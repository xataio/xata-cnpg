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
	"os"
	"path/filepath"
	"testing"
)

func TestMoveToOwnDir(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "manager")
	dir := filepath.Join(root, "bin")
	if err := os.WriteFile(file, []byte("v1"), 0o600); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	// Leftovers of an interrupted call.
	if err := os.WriteFile(file+".link", nil, 0o600); err != nil {
		t.Fatal(err)
	}

	// The second call must find the link and do nothing.
	for range 2 {
		if err := moveToOwnDir(file, dir); err != nil {
			t.Fatal(err)
		}
	}

	info, err := os.Lstat(file)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("%s is not a symbolic link", file)
	}
	after, err := os.Stat(filepath.Join(dir, "manager"))
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(before, after) {
		t.Error("the file in the new directory is not the same inode")
	}
	if _, err := os.Lstat(file + ".link"); !os.IsNotExist(err) {
		t.Errorf("the temporary link is still there: %v", err)
	}
}
