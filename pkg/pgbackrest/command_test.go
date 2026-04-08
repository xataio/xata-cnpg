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
	"errors"
	"testing"
)

func TestCommandError_Error(t *testing.T) {
	err := &CommandError{
		Command:  "backup",
		Args:     []string{"--config=/controller/pgbackrest/pgbackrest.conf", "--stanza=test", "backup"},
		Stderr:   "ERROR: some pgbackrest error",
		ExitCode: 1,
	}

	msg := err.Error()
	if msg != "pgbackrest backup failed (exit code 1): ERROR: some pgbackrest error" {
		t.Errorf("unexpected error message: %s", msg)
	}
}

func TestCommandError_ErrorEmptyStderr(t *testing.T) {
	err := &CommandError{
		Command:  "stanza-create",
		ExitCode: 2,
	}

	msg := err.Error()
	if msg != "pgbackrest stanza-create failed (exit code 2): " {
		t.Errorf("unexpected error message: %s", msg)
	}
}

func TestCommandError_IsError(t *testing.T) {
	var err error = &CommandError{
		Command:  "archive-push",
		ExitCode: 1,
	}

	var cmdErr *CommandError
	if !errors.As(err, &cmdErr) {
		t.Error("expected CommandError to satisfy errors.As")
	}
	if cmdErr.ExitCode != 1 {
		t.Errorf("expected exit code 1, got %d", cmdErr.ExitCode)
	}
}

func TestErrWALNotFound(t *testing.T) {
	if ErrWALNotFound.Error() != "WAL segment not found in repository" {
		t.Errorf("unexpected error message: %s", ErrWALNotFound.Error())
	}
}

func TestConstants(t *testing.T) {
	if ConfigFilePath != "/controller/pgbackrest/pgbackrest.conf" {
		t.Errorf("unexpected ConfigFilePath: %s", ConfigFilePath)
	}
	if SpoolPath != "/controller/pgbackrest-spool" {
		t.Errorf("unexpected SpoolPath: %s", SpoolPath)
	}
}
