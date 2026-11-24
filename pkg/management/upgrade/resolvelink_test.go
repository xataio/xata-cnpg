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

package upgrade

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveLink(t *testing.T) {
	root := t.TempDir()
	// The temporary directory can itself be behind a symbolic link.
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, "bin", "manager")
	link := filepath.Join(root, "manager")
	if err := os.Mkdir(filepath.Dir(file), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	if got := resolveLink(file); got != file {
		t.Errorf("regular file: got %q, want %q", got, file)
	}
	if err := os.Symlink(file, link); err != nil {
		t.Fatal(err)
	}
	if got := resolveLink(link); got != file {
		t.Errorf("link: got %q, want %q", got, file)
	}
	missing := filepath.Join(root, "missing")
	if got := resolveLink(missing); got != missing {
		t.Errorf("missing file: got %q, want %q", got, missing)
	}
}
