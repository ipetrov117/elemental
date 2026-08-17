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

	"github.com/urfave/cli/v3"
)

type ConfigureFlags struct {
	Path string
}

var ConfigureArgs ConfigureFlags

func NewConfigureCommand(appName string, action func(context.Context, *cli.Command) error) *cli.Command {
	return &cli.Command{
		Name:      "apply",
		Usage:     "Apply custom configurations over a running node",
		UsageText: fmt.Sprintf("%s apply", appName),
		Action:    action,
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:        "path",
				Usage:       "Path to a local file containing the configuration",
				Destination: &ConfigureArgs.Path,
			},
		},
	}
}
