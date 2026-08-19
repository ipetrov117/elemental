/*
Copyright © 2025-2026 SUSE LLC
SPDX-License-Identifier: Apache-2.0

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

	http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package elemental3cfg

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/suse/elemental/v3/internal/cli/cmd/elemental3cfg"
	"github.com/suse/elemental/v3/internal/image"
	"github.com/suse/elemental/v3/pkg/sys"
	"github.com/urfave/cli/v3"
	"gopkg.in/yaml.v3"
)

func Configure(ctx context.Context, cmd *cli.Command) error {
	if cmd.Root().Metadata == nil || cmd.Root().Metadata["system"] == nil {
		return fmt.Errorf("error setting up initial configuration")
	}
	system := cmd.Root().Metadata["system"].(*sys.System)
	logger := system.Logger()
	runner := system.Runner()
	// fs := system.FS()
	args := &elemental3cfg.ConfigureArgs

	cfg, err := parseConfig(args.Path)
	if err != nil {
		logger.Error("Parsing runtime configuration has failed")
		return err
	}

	if cfg.Hostname != "" {
		if err := setHostname(runner, cfg.Hostname); err != nil {
			logger.Error("Setting hostname for system has failed")
			return err
		}
	}

	if cfg.RKE2 != nil {
		if err := writeDropIns(cfg.RKE2); err != nil {
			logger.Error("Writing node runtime configurations")
			return err
		}
	}

	return nil
}

type RuntimeConfig struct {
	Hostname string `yaml:"hostname,omitempty"`
	RKE2     *RKE2  `yaml:"rke2,omitempty"`
}

type RKE2 struct {
	Type   string         `yaml:"type,omitempty"`
	Init   bool           `yaml:"init,omitempty"`
	Config map[string]any `yaml:"config,omitempty"`
}

func parseConfig(path string) (*RuntimeConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config: %w", err)
	}

	var cfg RuntimeConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}
	return &cfg, nil
}

func setHostname(runner sys.Runner, host string) error {
	const cmd = "hostnamectl"
	args := []string{"set-hostname", host}

	_, err := runner.Run(cmd, args...)
	if err != nil {
		return err
	}
	return nil
}

func writeDropIns(rke2 *RKE2) error {
	if err := writeElementalK8sConfigDropIn(rke2); err != nil {
		return fmt.Errorf("writing systemd drop-in file: %w", err)
	}

	if err := writeElementalK8sResourceDropIn(rke2); err != nil {
		return fmt.Errorf("writing systemd drop-in file: %w", err)
	}

	return writeRKE2ConfigDropIn(rke2)
}

func writeElementalK8sConfigDropIn(rke2 *RKE2) error {
	const (
		dropInDir     = "/etc/systemd/system/k8s-config-installer.service.d"
		dropInName    = "runtime.conf"
		dropInContent = `[Service]
EnvironmentFile=-%s
`
		envName = "runtime.env"
	)

	envs := []string{
		fmt.Sprintf("IS_INIT_NODE=%t", rke2.Init),
		fmt.Sprintf("NODETYPE=%s", rke2.Type),
	}

	envPath := filepath.Join("/", image.ElementalPath(), envName)
	if err := writeFile(envPath, strings.Join(envs, "\n")+"\n"); err != nil {
		return fmt.Errorf("writing %s env file: %w", dropInName, err)
	}

	return writeFile(filepath.Join(dropInDir, dropInName), fmt.Sprintf(dropInContent, envPath))
}

func writeElementalK8sResourceDropIn(rke2 *RKE2) error {
	const (
		initTrigger   = "init-node"
		dropInDir     = "/etc/systemd/system/k8s-resource-installer.service.d"
		dropInName    = "runtime.conf"
		dropInContent = `[Unit]
ConditionHost=
ConditionPathExists=%s
`
	)

	initTriggerPath := filepath.Join("/", image.ElementalPath(), initTrigger)
	if rke2.Init {
		if err := writeFile(initTriggerPath, ""); err != nil {
			return fmt.Errorf("writing init trigger file: %w", err)
		}
	}

	return writeFile(filepath.Join(dropInDir, dropInName), fmt.Sprintf(dropInContent, initTriggerPath))
}

func writeRKE2ConfigDropIn(rke2 *RKE2) error {
	const (
		dropInDir  = "/etc/rancher/rke2/config.yaml.d"
		dropInName = "99-elemental-runtime-config.yaml"
	)

	if rke2.Config == nil {
		return nil
	}

	content, err := yaml.Marshal(rke2.Config)
	if err != nil {
		return fmt.Errorf("marshaling runtime RKE2 configuration: %w", err)
	}

	return writeFile(filepath.Join(dropInDir, dropInName), string(content))
}

func writeFile(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("creating file directory: %w", err)
	}

	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return fmt.Errorf("writing file at path %q: %w", path, err)
	}

	return nil
}
