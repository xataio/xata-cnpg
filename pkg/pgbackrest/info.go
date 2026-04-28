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
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"

	"github.com/cloudnative-pg/machinery/pkg/log"
)

// StanzaInfo represents a stanza in pgbackrest info JSON output.
type StanzaInfo struct {
	Name   string       `json:"name"`
	Backup []BackupInfo `json:"backup"`
	Status struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"status"`
}

// BackupInfo represents a single backup in pgbackrest info JSON output.
type BackupInfo struct {
	Label   string `json:"label"`
	Type    string `json:"type"`
	Archive struct {
		Start string `json:"start"`
		Stop  string `json:"stop"`
	} `json:"archive"`
	LSN struct {
		Start string `json:"start"`
		Stop  string `json:"stop"`
	} `json:"lsn"`
	Timestamp struct {
		Start int64 `json:"start"`
		Stop  int64 `json:"stop"`
	} `json:"timestamp"`
	Info struct {
		Size       int64 `json:"size"`
		Delta      int64 `json:"delta"`
		Repository struct {
			Size  int64 `json:"size"`
			Delta int64 `json:"delta"`
		} `json:"repository"`
	} `json:"info"`
	Error bool `json:"error"`
}

// LatestBackup returns the last backup in the list, or nil if empty.
func (s *StanzaInfo) LatestBackup() *BackupInfo {
	if len(s.Backup) == 0 {
		return nil
	}
	return &s.Backup[len(s.Backup)-1]
}

// Info runs pgbackrest info and returns the parsed stanza info.
// pgbackrest always returns an array but with --stanza it contains exactly one element.
func Info(ctx context.Context, stanzaName string) (*StanzaInfo, error) {
	contextLog := log.FromContext(ctx)

	fullArgs := []string{
		"--config=" + ConfigFilePath,
		"--stanza=" + stanzaName,
		"info",
		"--output=json",
	}

	contextLog.Debug("Running pgbackrest info", "args", fullArgs)

	cmd := exec.CommandContext(ctx, pgbackrestBinary, fullArgs...) // #nosec G204

	stdout, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return nil, &CommandError{
				Command:  "info",
				Args:     fullArgs,
				Stderr:   string(exitErr.Stderr),
				ExitCode: exitErr.ExitCode(),
			}
		}
		return nil, fmt.Errorf("failed to execute pgbackrest info: %w", err)
	}

	var result []StanzaInfo
	if err := json.Unmarshal(stdout, &result); err != nil {
		return nil, fmt.Errorf("parsing pgbackrest info JSON: %w", err)
	}

	if len(result) == 0 {
		return nil, fmt.Errorf("pgbackrest info returned no stanzas for %s", stanzaName)
	}

	return &result[0], nil
}
