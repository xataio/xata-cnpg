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
)

const (
	// managerPath is the instance manager binary that the pod, the
	// archive_command and the restore_command execute.
	managerPath = "/controller/manager"

	// managerDir holds the instance manager binary when Landlock is in use.
	managerDir = "/controller/bin"
)

// moveToOwnDir links file into dir, and replaces file with a symbolic link
// to the new name. It does nothing when file is a symbolic link already.
//
// PostgreSQL must read and execute the instance manager binary for
// archive_command. A Landlock rule on the file does not survive an online
// upgrade, which replaces the file with a new inode, and the rules of a
// running process tree cannot change. A rule on /controller is not possible,
// because it would also cover the ServiceAccount token. A rule on a
// directory of its own covers each later binary, and PostgreSQL has no
// right to write there.
func moveToOwnDir(file, dir string) error {
	info, err := os.Lstat(file)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil
	}

	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	// The hard link keeps the inode of the running binary. A file with
	// one of the two names is what an earlier, interrupted call left.
	target := filepath.Join(dir, filepath.Base(file))
	if err := os.Remove(target); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := os.Link(file, target); err != nil {
		return err
	}
	link := file + ".link"
	if err := os.Remove(link); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := os.Symlink(target, link); err != nil {
		return err
	}
	return os.Rename(link, file)
}
