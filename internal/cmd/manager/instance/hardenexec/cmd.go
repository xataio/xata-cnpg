/*
Copyright © contributors to CloudNativePG, established as
CloudNativePG a Series of LF Projects, LLC.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.

SPDX-License-Identifier: Apache-2.0
*/

// Package hardenexec implements the "instance harden-exec" subcommand
package hardenexec

import (
	"github.com/spf13/cobra"

	"github.com/xataio/xata-cnpg/pkg/management/hardening"
)

// NewCmd creates the "instance harden-exec" subcommand
func NewCmd() *cobra.Command {
	var featureList string

	cmd := &cobra.Command{
		Use:    "harden-exec --features <list> -- <path> <argv0> [args]",
		Short:  "Apply kernel restrictions, then execute a program",
		Hidden: true,
		Args:   cobra.MinimumNArgs(2),
		RunE: func(_ *cobra.Command, args []string) error {
			features, err := hardening.Parse(featureList)
			if err != nil {
				return err
			}
			return hardening.Exec(features, args[0], args[1:])
		},
	}
	cmd.Flags().StringVar(&featureList, "features", "", "Comma-separated list of the features to apply")

	return cmd
}
