/*
Copyright © 2025 SUSE LLC
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

package build

import (
	"context"
	_ "embed"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/suse/elemental/v3/internal/cli/elemental-toolkit/action"
	"github.com/suse/elemental/v3/internal/cli/elemental-toolkit/cmd"
	"github.com/suse/elemental/v3/internal/image"
	"github.com/suse/elemental/v3/internal/manifest/extractor"
	"github.com/suse/elemental/v3/internal/template"
	"github.com/suse/elemental/v3/pkg/deployment"
	"github.com/suse/elemental/v3/pkg/firmware"
	"github.com/suse/elemental/v3/pkg/install"
	"github.com/suse/elemental/v3/pkg/log"
	"github.com/suse/elemental/v3/pkg/manifest/api"
	"github.com/suse/elemental/v3/pkg/manifest/resolver"
	"github.com/suse/elemental/v3/pkg/manifest/source"
	"github.com/suse/elemental/v3/pkg/sys"
	"github.com/suse/elemental/v3/pkg/sys/runner"
	"github.com/suse/elemental/v3/pkg/sys/vfs"
	"github.com/suse/elemental/v3/pkg/unpack"
	"gopkg.in/yaml.v3"
)

const (
	coreReleaseManifestRef = "registry.opensuse.org/home/ipetrov117/branches/isv/suse/edge/lifecycle/containerfile/release-manifest-core"
	configScriptName       = "config.sh"
	k8sResDeployScriptName = "k8s_res_deploy.sh"
	defaultSize            = "6G"
)

//go:embed templates/config.sh.tpl
var configScriptTpl string

//go:embed templates/k8s_res_deploy.sh.tpl
var k8sResDeployScriptTpl string

func Run(ctx context.Context, d *image.Definition, buildDir string, l log.Logger, local bool, configDir image.ConfigDir) error {
	var overlaysDir = filepath.Join(buildDir, "overlays")

	m, err := resolveManifest(d.Release.ManifestURI, buildDir)
	if err != nil {
		l.Error("Resolving release manifest")
		return err
	}

	runtimeK8sResDeployScript, err := prepareForK8sResourceDeploy(overlaysDir, configDir.K8sManifestsPath(), d, m)
	if err != nil {
		l.Error("Preparing for Kuberentes resource deployment")
		return err
	}

	scriptPath, err := writeConfigScript(buildDir, d.OperatingSystem.Users, runtimeK8sResDeployScript)
	if err != nil {
		l.Error("Preparing configuration script")
		return err
	}

	fmt.Println(scriptPath)

	// RAW image
	runner := runner.NewRunner(runner.WithLogger(l))
	if err := createRaw(runner, d.Image, d.OperatingSystem.RawConfig); err != nil {
		l.Error("Creating raw image")
		return err
	}

	// LOOP device
	device, err := setupLoopDevice(runner, d.Image)
	if err != nil {
		l.Error("Setting up loop device for %s", d.Image.OutputImageName)
		return err
	}

	// OVERLAY setup
	if err := addRKE2ToOverlays(m.CorePlatform.Components.Kubernetes.RKE2.Image, overlaysDir); err != nil {
		l.Error("Preparing RKE2 extension")
		return err
	}

	mountClean, err := addCertificatesToOverlays(runner, overlaysDir)
	defer func() error {
		for _, clean := range mountClean {
			cleanErr := clean()
			if cleanErr != nil {
				panic(cleanErr)
			}
		}
		return nil
	}()
	if err != nil {
		l.Error("Preparing certificates extension")
		return err
	}

	// ARCHIVE overlays
	overlaysTar := filepath.Join(buildDir, "overlays.tar.gz")
	if err = tar(runner, overlaysTar, overlaysDir); err != nil {
		l.Error("Archiving OS overlays")
		return err
	}

	// INSTALL prep
	s, err := sys.NewSystem(sys.WithLogger(l))
	if err != nil {
		l.Error("Setting up system")
		return err
	}

	imgSourceType := deployment.OCI
	imgSource := m.CorePlatform.Components.OperatingSystem.Image
	// Unpacking logic should not be here by default, this is mainly so that
	// we do not have to pull the image on each run, which is extremely time consuming..
	if local {
		unpacker := unpack.NewOCIUnpacker(s, imgSource, unpack.WithLocal(true))
		osUnpackDir := filepath.Join(buildDir, "os-unpack")
		if err := os.MkdirAll(osUnpackDir, 0755); err != nil {
			l.Error("Creating OS unpack directory")
			return err
		}

		_, err := unpacker.Unpack(ctx, osUnpackDir)
		if err != nil {
			l.Error("Unpacking OS image '%s'", imgSource)
			return err
		}

		imgSource = osUnpackDir
		imgSourceType = deployment.Dir
	}

	installFlags := &cmd.InstallFlags{
		OperatingSystemImage: fmt.Sprintf("%s://%s", imgSourceType, imgSource),
		ConfigScript:         scriptPath,
		Target:               device,
		Overlay:              fmt.Sprintf("%s://%s", deployment.Tar, overlaysTar),
	}

	dep, err := action.DigestInstallSetup(s, installFlags)
	if err != nil {
		l.Error("Preparing installation setup")
		return err
	}

	// ACTUAL INSTALL
	manager := firmware.NewEfiBootManager(s)
	installer := install.New(ctx, s, install.WithBootManager(manager))
	err = installer.Install(dep)
	if err != nil {
		s.Logger().Error("installation failed: %v", err)
		return err
	}

	defer func() {
		err := detatchLoop(runner, device)
		if err != nil {
			panic(err)
		}
	}()
	return nil
}

func resolveManifest(manifestURI, storeDir string) (*resolver.ResolvedManifest, error) {
	manifestStore := filepath.Join(storeDir, "release-manifest-store")
	if err := os.MkdirAll(manifestStore, os.ModeDir); err != nil {
		return nil, fmt.Errorf("creating release manfiest store '%s': %w", manifestStore, err)
	}

	extr, err := extractor.New(extractor.WithStore(manifestStore))
	if err != nil {
		return nil, fmt.Errorf("initialising OCI release manifest extractor: %w", err)
	}

	res := resolver.New(source.NewReader(extr), coreReleaseManifestRef)
	m, err := res.Resolve(manifestURI)
	if err != nil {
		return nil, fmt.Errorf("resolving manifest at uri '%s': %w", manifestURI, err)
	}

	return m, nil
}

func writeConfigScript(buildDir string, users []image.User, runtimeK8sResDeployScript string) (path string, err error) {
	values := struct {
		Users                []image.User
		KubernetesDir        string
		ManifestDeployScript string
	}{
		Users: users,
	}

	if runtimeK8sResDeployScript != "" {
		values.KubernetesDir = filepath.Dir(runtimeK8sResDeployScript)
		values.ManifestDeployScript = runtimeK8sResDeployScript
	}

	data, err := template.Parse(configScriptName, configScriptTpl, &values)
	if err != nil {
		return "", fmt.Errorf("parsing config script template: %w", err)
	}

	filename := filepath.Join(buildDir, configScriptName)
	if err := os.WriteFile(filename, []byte(data), os.FileMode(0o744)); err != nil {
		return "", fmt.Errorf("writing config script: %w", err)
	}
	return filename, nil
}

func createRaw(run sys.Runner, img image.Image, rawConfig image.RawConfiguration) error {
	diskSize := rawConfig.DiskSize
	if diskSize == "" {
		diskSize = defaultSize
	} else if !diskSize.IsValid() {
		return fmt.Errorf("invalid disk size definition '%s'", diskSize)
	}

	qemuImg := "qemu-img"
	qemuImgArgs := []string{"create", "-f", "raw", img.OutputImageName, string(diskSize)}
	_, err := run.Run(qemuImg, qemuImgArgs...)
	if err != nil {
		return fmt.Errorf("creating raw image using '%s': %w", qemuImg, err)
	}

	return nil
}

func setupLoopDevice(run sys.Runner, img image.Image) (device string, err error) {
	losetup := "losetup"
	losetupArgs := []string{"-f", "--show", img.OutputImageName}
	out, err := run.Run(losetup, losetupArgs...)
	if err != nil {
		return "", fmt.Errorf("setting up loop device using '%s': %w", losetup, err)
	}

	loopDevice := strings.TrimSpace(string(out))
	if !regexp.MustCompile(`^/dev/loop[0-9]+$`).MatchString(loopDevice) {
		return "", fmt.Errorf("unexpected loop device format: '%s'", loopDevice)
	}

	return loopDevice, nil
}

func detatchLoop(run sys.Runner, device string) error {
	losetup := "losetup"
	losetupArgs := []string{"-d", device}

	_, err := run.Run(losetup, losetupArgs...)
	if err != nil {
		return fmt.Errorf("detatching loop device '%s': %w", device, err)
	}

	return nil
}

func addRKE2ToOverlays(rke2URL, overlays string) error {
	extensionsPath := filepath.Join(overlays, "etc", "extensions")
	if err := os.MkdirAll(extensionsPath, os.ModeDir); err != nil {
		return fmt.Errorf("setting up extensions directory '%s': %w", extensionsPath, err)
	}

	parsedURL, err := url.Parse(rke2URL)
	if err != nil {
		return fmt.Errorf("invalid url '%s': %w", rke2URL, err)
	}

	fileName := filepath.Base(parsedURL.Path)
	output := filepath.Join(extensionsPath, fileName)

	outFile, err := os.Create(output)
	if err != nil {
		return fmt.Errorf("creating output file '%s': %w", output, err)
	}
	defer func() { _ = outFile.Close() }()

	resp, err := http.Get(rke2URL)
	if err != nil {
		fmt.Println("Download error:", err)
		return fmt.Errorf("downloading rke2 raw file: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("rke2 raw file download failed: %v", resp.StatusCode)
	}

	_, err = io.Copy(outFile, resp.Body)
	if err != nil {
		return fmt.Errorf("copying content to '%s': %w", outFile.Name(), err)
	}

	return nil
}

// Temporary testing fix must be removed for non-PoC build logic
func addCertificatesToOverlays(run sys.Runner, overlays string) (mountClean []func() error, err error) {
	hostSSL := filepath.Join(string(os.PathSeparator), "etc", "ssl")
	hostCACerts := filepath.Join(string(os.PathSeparator), "var", "lib", "ca-certificates")
	overlaySSL := filepath.Join(overlays, hostSSL)
	overlayCACerts := filepath.Join(overlays, hostCACerts)

	if err := os.MkdirAll(overlaySSL, os.ModeDir); err != nil {
		return nil, fmt.Errorf("creating 'ssl' directory '%s': %w", hostSSL, err)
	}

	if err := os.MkdirAll(overlayCACerts, os.ModeDir); err != nil {
		return nil, fmt.Errorf("creating 'ca-certificates' directory '%s'; %w", hostCACerts, err)
	}

	mountClean = make([]func() error, 0)
	if err = mount(run, hostSSL, overlaySSL); err != nil {
		return nil, fmt.Errorf("mounting '%s' to '%s': %w", hostSSL, overlaySSL, err)
	}
	mountClean = append(mountClean, func() error {
		return umount(run, overlaySSL)
	})

	if err = mount(run, hostCACerts, overlayCACerts); err != nil {
		return nil, fmt.Errorf("mounting '%s' to '%s': %w", hostCACerts, overlayCACerts, err)
	}
	mountClean = append(mountClean, func() error {
		return umount(run, overlayCACerts)
	})

	return mountClean, nil
}

// Temporary testing fix must be removed for non-PoC build logic
func mount(run sys.Runner, src, dest string) error {
	mount := "mount"
	mountArgs := []string{"--bind", src, dest}
	_, err := run.Run(mount, mountArgs...)
	if err != nil {
		return fmt.Errorf("mounting '%s' to '%s' : %w", src, dest, err)
	}

	return nil
}

// Temporary testing fix must be removed for non-PoC build logic
func umount(run sys.Runner, dest string) error {
	_, err := run.Run("umount", dest)
	if err != nil {
		return fmt.Errorf("unmounting '%s': %w", dest, err)
	}

	return nil
}

// Temporary testing fix must be removed for non-PoC build logic
func tar(run sys.Runner, tarPath, rootDir string) error {
	tar := "tar"
	tarArgs := []string{"-cavf", tarPath, "-C", rootDir, "."}
	_, err := run.Run(tar, tarArgs...)
	if err != nil {
		return fmt.Errorf("archiving '%s': %w", rootDir, err)
	}

	return nil
}

func prepareForK8sResourceDeploy(overlaysDir, localManifestsDir string, d *image.Definition, rm *resolver.ResolvedManifest) (runtimeScriptPath string, err error) {
	relativeKubernetesPath := filepath.Join("var", "lib", "unified-core", "kubernetes")
	runtimeHelmCharts, err := prepareHelmCharts(overlaysDir, relativeKubernetesPath, d, rm)
	if err != nil {
		return "", fmt.Errorf("preparing helm chart resources: %w", err)
	}

	runtimeManifestsDir, err := prepareManifests(overlaysDir, relativeKubernetesPath, localManifestsDir, d)
	if err != nil {
		return "", fmt.Errorf("preparing Kubernetes manfiests: %w", err)
	}

	if runtimeManifestsDir == "" && len(runtimeHelmCharts) == 0 {
		return
	}

	overlayKubernetesPath := filepath.Join(overlaysDir, relativeKubernetesPath)
	scriptInOverlays, err := writeK8sResDeployScript(overlayKubernetesPath, runtimeManifestsDir, runtimeHelmCharts)
	if err != nil {
		return "", fmt.Errorf("preparing script: %w", err)
	}

	return filepath.Join(string(os.PathSeparator), strings.TrimPrefix(scriptInOverlays, overlaysDir)), nil
}

func prepareHelmCharts(overlaysRoot, relativeK8sPath string, d *image.Definition, rm *resolver.ResolvedManifest) (runtimeHelmCharts []string, err error) {
	relativeHelmPath := filepath.Join(relativeK8sPath, "helm")
	overlayHelmPath := filepath.Join(overlaysRoot, relativeHelmPath)
	rootHelmPath := filepath.Join(string(os.PathSeparator), relativeHelmPath)

	helmConfigs := prepareHelmConfigs(&d.Kubernetes, rm)
	if len(helmConfigs) == 0 {
		return
	}

	chartNames, err := writeHelmCharts(overlayHelmPath, helmConfigs)
	if err != nil {
		return nil, fmt.Errorf("writing helm chart resources to %s: %w", overlayHelmPath, err)
	}

	for _, chartName := range chartNames {
		runtimeHelmCharts = append(runtimeHelmCharts, filepath.Join(rootHelmPath, chartName))
	}

	return runtimeHelmCharts, nil
}

func prepareHelmConfigs(k *image.Kubernetes, rm *resolver.ResolvedManifest) []*api.Helm {
	configs := []*api.Helm{}

	if rm.CorePlatform != nil && rm.CorePlatform.Components.Helm != nil {
		configs = append(configs, rm.CorePlatform.Components.Helm)
	}

	if rm.ProductExtension != nil && rm.ProductExtension.Components.Helm != nil {
		configs = append(configs, rm.ProductExtension.Components.Helm)
	}

	if k.Helm != nil {
		configs = append(configs, k.Helm)
	}

	return configs
}

func writeHelmCharts(dest string, configs []*api.Helm) (names []string, err error) {
	names = []string{}
	if configs == nil {
		return names, nil
	}

	if err := os.MkdirAll(dest, os.ModeDir); err != nil {
		return nil, fmt.Errorf("setting up HelmChart resources directory '%s': %w", dest, err)
	}

	for _, config := range configs {
		for _, helmCRD := range ProduceCRDs(config) {
			data, err := yaml.Marshal(helmCRD)
			if err != nil {
				return nil, fmt.Errorf("marshaling helm chart: %w", err)
			}

			chartName := fmt.Sprintf("%s.yaml", helmCRD.Metadata.Name)
			chartPath := filepath.Join(dest, chartName)
			if err = os.WriteFile(chartPath, data, os.FileMode(0o644)); err != nil {
				return nil, fmt.Errorf("writing helm chart: %w", err)
			}

			names = append(names, chartName)
		}
	}

	return names, nil
}

func prepareManifests(overlaysRoot, relativeK8sPath, localManifestsDir string, d *image.Definition) (runtimeManifestsDir string, err error) {
	var localManifestsExist bool
	if _, err := os.Stat(localManifestsDir); err != nil && !os.IsNotExist(err) {
		return "", fmt.Errorf("validating local manifest directory %s: %w", localManifestsDir, err)
	} else {
		localManifestsExist = err == nil
	}

	if !localManifestsExist || len(d.Kubernetes.Manifests) == 0 {
		return
	}

	relativeManifestsPath := filepath.Join(relativeK8sPath, "manifests")
	overlayManifestsPath := filepath.Join(overlaysRoot, relativeManifestsPath)

	// TODO: REMOTE MANIFESTS ARE MISSING HERE, ADD THEM AFTER REBASE!!
	if err := writeManifests(overlayManifestsPath, localManifestsDir); err != nil {
		return "", fmt.Errorf("writing Kubernetes manifests to %s: %w", overlayManifestsPath, err)
	}

	return filepath.Join(string(os.PathSeparator), relativeManifestsPath), nil
}

// TODO: Add REMOTE MANIFESTS AFTER REBASE
func writeManifests(dest string, localManifestsDir string) error {
	entries, err := os.ReadDir(localManifestsDir)
	if err != nil {
		return fmt.Errorf("reading local manifests directory '%s': %w", localManifestsDir, err)
	}

	if err := os.MkdirAll(dest, os.ModeDir); err != nil {
		return fmt.Errorf("setting up manifests directory '%s': %w", dest, err)
	}

	fs := vfs.New()
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		localManifest := filepath.Join(localManifestsDir, entry.Name())
		copyPath := filepath.Join(dest, entry.Name())
		if err := vfs.CopyFile(fs, localManifest, copyPath); err != nil {
			return fmt.Errorf("copying file %s to %s: %w", localManifest, copyPath, err)
		}
	}
	return nil
}

func writeK8sResDeployScript(dest, runtimeManifestsDir string, runtimeHelmCharts []string) (path string, err error) {
	values := struct {
		HelmCharts   []string
		ManifestsDir string
	}{
		HelmCharts:   runtimeHelmCharts,
		ManifestsDir: runtimeManifestsDir,
	}

	data, err := template.Parse(k8sResDeployScriptName, k8sResDeployScriptTpl, &values)
	if err != nil {
		return "", fmt.Errorf("parsing template for %s: %w", k8sResDeployScriptName, err)
	}

	filename := filepath.Join(dest, k8sResDeployScriptName)
	if err := os.WriteFile(filename, []byte(data), os.FileMode(0o744)); err != nil {
		return "", fmt.Errorf("writing %s: %w", filename, err)
	}
	return filename, nil
}
