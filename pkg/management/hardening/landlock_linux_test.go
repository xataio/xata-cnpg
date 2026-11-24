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
	"slices"
	"testing"
)

func TestCoversServiceAccount(t *testing.T) {
	tests := []struct {
		dir  string
		want bool
	}{
		{"/", true},
		{"/run", true},
		{"/run/", true},
		{"/var/run", true},
		{"/run/secrets", true},
		{"/var/run/secrets/kubernetes.io", true},
		{"/run/secrets/kubernetes.io/serviceaccount", true},
		{"/var/run/secrets/kubernetes.io/serviceaccount/..data", true},
		{"/run/secrets/eks.amazonaws.com/serviceaccount", false},
		{"/var/run/secrets/azure/tokens", false},
		{"/runner", false},
		{"/controller/run", false},
		{"/projected", false},
	}
	for _, tt := range tests {
		if got := coversServiceAccount(tt.dir); got != tt.want {
			t.Errorf("coversServiceAccount(%q) = %v, want %v", tt.dir, got, tt.want)
		}
	}
}

func TestLandlockRulesDoNotCoverTheToken(t *testing.T) {
	// The scratch volume is mounted on all three, and the token is below
	// /run, so a rule on one of them covers the token.
	scratchMounts := []string{"/run", "/tmp", "/controller"}

	for _, dir := range slices.Concat(readOnlyDirs, writableDirs, writableFileDirs) {
		if coversServiceAccount(dir) || slices.Contains(scratchMounts, dir) {
			t.Errorf("a rule on %q exposes the ServiceAccount token", dir)
		}
	}
}

func TestTokenDirs(t *testing.T) {
	t.Setenv("AWS_WEB_IDENTITY_TOKEN_FILE", "/var/run/secrets/eks.amazonaws.com/serviceaccount/token")
	t.Setenv("AZURE_FEDERATED_TOKEN_FILE", "")
	dirs, err := tokenDirs()
	if err != nil {
		t.Fatal(err)
	}
	// /var/run can be a link to /run on the machine that runs the test.
	if len(dirs) != 1 || coversServiceAccount(dirs[0]) {
		t.Errorf("tokenDirs() = %v", dirs)
	}

	t.Setenv("AZURE_FEDERATED_TOKEN_FILE", "/var/run/secrets/kubernetes.io/serviceaccount/token")
	if _, err := tokenDirs(); err == nil {
		t.Error("tokenDirs() accepted the directory of the ServiceAccount token")
	}
}
