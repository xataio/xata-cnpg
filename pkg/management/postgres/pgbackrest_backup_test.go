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

package postgres

import "testing"

func TestProgressRegex(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			"running backup with progress",
			"ok (backup/expire running - 56.73% complete)",
			"56.73",
		},
		{
			"nearly complete",
			"ok (backup/expire running - 99.99% complete)",
			"99.99",
		},
		{
			"just started",
			"ok (backup/expire running - 0.01% complete)",
			"0.01",
		},
		{
			"no progress info",
			"ok",
			"",
		},
		{
			"empty string",
			"",
			"",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			matches := progressRegex.FindStringSubmatch(tt.input)
			if tt.expected == "" {
				if len(matches) >= 2 {
					t.Errorf("expected no match, got %s", matches[1])
				}
			} else {
				if len(matches) < 2 {
					t.Errorf("expected match %s, got none", tt.expected)
				} else if matches[1] != tt.expected {
					t.Errorf("expected %s, got %s", tt.expected, matches[1])
				}
			}
		})
	}
}
