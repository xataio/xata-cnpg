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
	WrapCommand(cmd, nil)
	if cmd.Path != "/usr/bin/postgres" || !slices.Equal(cmd.Args, []string{"/usr/bin/postgres", "-D", "/pgdata"}) {
		t.Errorf("command changed: %q %q", cmd.Path, cmd.Args)
	}
}
