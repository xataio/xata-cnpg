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

package hardening

import (
	"os/exec"
	"slices"
	"testing"
)

func TestParse(t *testing.T) {
	tests := []struct {
		in      string
		want    Features
		wantErr bool
	}{
		{in: "", want: nil},
		{in: " , ", want: nil},
		{in: "no-such-feature", wantErr: true},
		{in: "mdwe", want: Features{MDWE}},
		{in: " mdwe ,mdwe,", want: Features{MDWE}},
		{in: "mdwe,no-such-feature", wantErr: true},
		{in: "mdwe,no-dumpable", want: Features{NoDumpable, MDWE}},
		{in: "seccomp,mdwe", want: Features{MDWE, Seccomp}},
		{in: "seccomp,seccomp-log", wantErr: true},
	}
	for _, tt := range tests {
		got, err := Parse(tt.in)
		if (err != nil) != tt.wantErr {
			t.Errorf("Parse(%q) error = %v, want error %v", tt.in, err, tt.wantErr)
			continue
		}
		if !slices.Equal(got, tt.want) {
			t.Errorf("Parse(%q) = %v, want %v", tt.in, got, tt.want)
		}
		if again, err := Parse(got.String()); err != nil || !slices.Equal(again, got) {
			t.Errorf("Parse(%q) = %v, %v, want %v", got.String(), again, err, got)
		}
	}
}

func TestWrapCommandWithoutFeatures(t *testing.T) {
	cmd := exec.Command("/usr/bin/postgres", "-D", "/pgdata")
	WrapCommand(cmd, nil, "0x31")
	if cmd.Path != "/usr/bin/postgres" || !slices.Equal(cmd.Args, []string{"/usr/bin/postgres", "-D", "/pgdata"}) {
		t.Errorf("command changed: %q %q", cmd.Path, cmd.Args)
	}
}

func TestWrapCommand(t *testing.T) {
	tests := []struct {
		name     string
		features Features
		filter   string
		want     []string // nil: the command does not change
	}{
		{
			name:     "process tree feature",
			features: Features{MDWE},
			filter:   "0x31",
			want: []string{
				selfExe, "instance", "harden-exec", "--features", "mdwe",
				"--", "/usr/lib/postgresql/17/bin/postgres", "postgres", "-D", "/pgdata",
			},
		},
		{
			name:     "manager feature with a filter",
			features: Features{NoDumpable},
			filter:   "0x31",
			want: []string{
				selfExe, "instance", "harden-exec", "--features", "", "--coredump-filter", "0x31",
				"--", "/usr/lib/postgresql/17/bin/postgres", "postgres", "-D", "/pgdata",
			},
		},
		{
			name:     "manager feature without a filter",
			features: Features{NoDumpable},
		},
		{
			name:     "both",
			features: Features{NoDumpable, MDWE},
			filter:   "0x31",
			want: []string{
				selfExe, "instance", "harden-exec", "--features", "mdwe", "--coredump-filter", "0x31",
				"--", "/usr/lib/postgresql/17/bin/postgres", "postgres", "-D", "/pgdata",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// As exec.Command leaves it after a PATH lookup: argv[0] is
			// the name that the caller gave.
			cmd := &exec.Cmd{
				Path: "/usr/lib/postgresql/17/bin/postgres",
				Args: []string{"postgres", "-D", "/pgdata"},
			}
			WrapCommand(cmd, tt.features, tt.filter)

			if tt.want == nil {
				if cmd.Path != "/usr/lib/postgresql/17/bin/postgres" {
					t.Errorf("command changed: %q %q", cmd.Path, cmd.Args)
				}
				return
			}
			if cmd.Path != selfExe || !slices.Equal(cmd.Args, tt.want) {
				t.Errorf("got %q %q, want %q", cmd.Path, cmd.Args, tt.want)
			}
		})
	}
}

func TestPostgresOptions(t *testing.T) {
	if got := (Features{NoDumpable}).PostgresOptions(); got != nil {
		t.Errorf("got %q, want none", got)
	}
	want := []string{"-c", "jit_provider=disabled_by_mdwe"}
	if got := (Features{MDWE}).PostgresOptions(); !slices.Equal(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}
