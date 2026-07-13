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

package pgbackrest

import (
	"bytes"
	"os"
	"slices"
	"strconv"

	"github.com/cloudnative-pg/machinery/pkg/fileutils"
	ps "github.com/mitchellh/go-ps"
)

// TLSServerPIDFile records the pgbackrest TLS server left running across an
// online instance manager upgrade so the replacement manager can adopt it.
const TLSServerPIDFile = dataDirectory + "/server.pid"

func checkForExistingTLSServer(pidFile string, serverExecutables ...string) (*os.Process, error) {
	contents, err := os.ReadFile(pidFile) //nolint:gosec // Production uses a fixed path; tests use temporary paths.
	if err == nil {
		pid, parseErr := strconv.Atoi(string(bytes.TrimSpace(contents)))
		if parseErr == nil {
			process, findErr := ps.FindProcess(pid)
			if findErr != nil {
				return nil, findErr
			}
			if process != nil && slices.Contains(serverExecutables, process.Executable()) {
				return os.FindProcess(pid)
			}
		}

		if removeErr := os.Remove(pidFile); removeErr != nil && !os.IsNotExist(removeErr) {
			return nil, removeErr
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}

	return nil, nil
}

func writeTLSServerPIDFile(pid int) error {
	_, err := fileutils.WriteFileAtomic(
		TLSServerPIDFile,
		[]byte(strconv.Itoa(pid)+"\n"),
		0o600,
	)
	return err
}

func removeTLSServerPIDFile(pid int) {
	contents, err := os.ReadFile(TLSServerPIDFile)
	if err != nil {
		return
	}
	filePID, err := strconv.Atoi(string(bytes.TrimSpace(contents)))
	if err == nil && filePID == pid {
		_ = os.Remove(TLSServerPIDFile)
	}
}
