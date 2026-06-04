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
	"fmt"
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

func TestArchiveGet_WALNotFound(t *testing.T) {
	// Simulate a CommandError with exit code 2 (WAL not found)
	cmdErr := &CommandError{
		Command:  "archive-get",
		ExitCode: 2,
		Stderr:   "WAL segment not found",
	}

	// Verify that exit code 2 maps to ErrWALNotFound
	var err error = cmdErr
	if !errors.As(err, &cmdErr) {
		t.Fatal("expected CommandError")
	}
	if cmdErr.ExitCode != 2 {
		t.Errorf("expected exit code 2, got %d", cmdErr.ExitCode)
	}

	// Verify ErrWALNotFound is a distinct error
	if errors.Is(cmdErr, ErrWALNotFound) {
		t.Error("CommandError should not be ErrWALNotFound directly")
	}
}

func TestIsStanzaMissingFromRepo(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			"FileMissingError with exit 103",
			&CommandError{
				Command:  "archive-push",
				ExitCode: 103,
				Stderr:   "repo1: [FileMissingError] unable to load info file '/stanza/archive/stanza/archive.info'",
			},
			true,
		},
		{
			"exit 103 without FileMissingError",
			&CommandError{
				Command:  "archive-push",
				ExitCode: 103,
				Stderr:   "repo1: [ArchiveMismatchError] PostgreSQL version 17, system-id 123 do not match",
			},
			false,
		},
		{
			"FileMissingError with different exit code",
			&CommandError{
				Command:  "archive-push",
				ExitCode: 1,
				Stderr:   "[FileMissingError] something",
			},
			false,
		},
		{
			"nil error",
			nil,
			false,
		},
		{
			"non-CommandError",
			fmt.Errorf("some error"),
			false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsStanzaMissingFromRepo(tt.err); got != tt.expected {
				t.Errorf("expected %v, got %v", tt.expected, got)
			}
		})
	}
}

func TestIsLockBusy(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			"lock acquire failure with exit 50",
			&CommandError{
				Command:  "backup",
				ExitCode: 50,
				Stderr:   "ERROR: [050]: unable to acquire lock: Resource temporarily unavailable",
			},
			true,
		},
		{
			"different exit code",
			&CommandError{
				Command:  "backup",
				ExitCode: 1,
				Stderr:   "some other failure",
			},
			false,
		},
		{
			"nil error",
			nil,
			false,
		},
		{
			"non-CommandError",
			fmt.Errorf("some error"),
			false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsLockBusy(tt.err); got != tt.expected {
				t.Errorf("expected %v, got %v", tt.expected, got)
			}
		})
	}
}
