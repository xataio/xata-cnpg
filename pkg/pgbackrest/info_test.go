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
	"encoding/json"
	"testing"
)

const sampleInfoJSON = `[
  {
    "name": "test-cluster",
    "backup": [
      {
        "label": "20260325-191631F",
        "type": "full",
        "archive": {
          "start": "000000010000003400000076",
          "stop": "000000010000003400000078"
        },
        "lsn": {
          "start": "34/76000028",
          "stop": "34/78000080"
        },
        "timestamp": {
          "start": 1742929591,
          "stop": 1742931337
        },
        "info": {
          "size": 274463129600,
          "delta": 274463129600,
          "repository": {
            "size": 30064771072,
            "delta": 30064771072
          }
        },
        "error": false
      },
      {
        "label": "20260325-200017F_20260326-085844D",
        "type": "diff",
        "archive": {
          "start": "0000000100000035000000ED",
          "stop": "0000000100000035000000EE"
        },
        "lsn": {
          "start": "35/ED000028",
          "stop": "35/EE000080"
        },
        "timestamp": {
          "start": 1742975924,
          "stop": 1742977316
        },
        "info": {
          "size": 274580570112,
          "delta": 274463129600,
          "repository": {
            "size": 30064771072,
            "delta": 30064771072
          }
        },
        "error": false
      }
    ],
    "status": {
      "code": 0,
      "message": "ok"
    }
  }
]`

const sampleInfoRunningJSON = `[
  {
    "name": "test-cluster",
    "backup": [],
    "status": {
      "code": 2,
      "message": "no valid backups",
      "lock": {
        "backup": {
          "held": true,
          "size": 109792819251,
          "size-cplt": 75161927680
        },
        "restore": {
          "held": false
        }
      }
    }
  }
]`

const sampleInfoRunningWithBackupsJSON = `[
  {
    "name": "test-cluster",
    "backup": [
      {
        "label": "20260423-085834F",
        "type": "full",
        "archive": {"start": "0000000100000014000000FC", "stop": "0000000100000014000000FD"},
        "lsn": {"start": "14/FC000028", "stop": "14/FD000050"},
        "timestamp": {"start": 1776934714, "stop": 1776935470},
        "info": {
          "size": 109792819513, "delta": 109792819513,
          "repository": {"size": 12035911988, "delta": 12035911988}
        },
        "error": false,
        "annotation": {"backup-cr": "perf-full-5vscm"}
      }
    ],
    "status": {
      "code": 0,
      "message": "ok",
      "lock": {
        "backup": {
          "held": true,
          "size": 109792819251,
          "size-cplt": 50000000000
        },
        "restore": {
          "held": false
        }
      }
    }
  }
]`

const sampleInfoEmptyJSON = `[
  {
    "name": "test-cluster",
    "backup": [],
    "status": {
      "code": 2,
      "message": "error (no valid backups)"
    }
  }
]`

func TestStanzaInfoParsing(t *testing.T) {
	var stanzas []StanzaInfo
	if err := json.Unmarshal([]byte(sampleInfoJSON), &stanzas); err != nil {
		t.Fatalf("failed to parse sample JSON: %v", err)
	}

	if len(stanzas) != 1 {
		t.Fatalf("expected 1 stanza, got %d", len(stanzas))
	}

	stanza := stanzas[0]
	if stanza.Name != "test-cluster" {
		t.Errorf("expected stanza name test-cluster, got %s", stanza.Name)
	}
	if stanza.Status.Code != 0 {
		t.Errorf("expected status code 0, got %d", stanza.Status.Code)
	}
	if stanza.Status.Message != "ok" {
		t.Errorf("expected status message ok, got %s", stanza.Status.Message)
	}
	if len(stanza.Backup) != 2 {
		t.Fatalf("expected 2 backups, got %d", len(stanza.Backup))
	}
	if stanza.Status.Lock != nil {
		t.Error("expected Lock to be nil when not present in JSON")
	}
}

func TestBackupInfoParsing(t *testing.T) {
	var stanzas []StanzaInfo
	if err := json.Unmarshal([]byte(sampleInfoJSON), &stanzas); err != nil {
		t.Fatalf("failed to parse: %v", err)
	}

	full := stanzas[0].Backup[0]
	if full.Label != "20260325-191631F" {
		t.Errorf("expected label 20260325-191631F, got %s", full.Label)
	}
	if full.Type != "full" {
		t.Errorf("expected type full, got %s", full.Type)
	}
	if full.Archive.Start != "000000010000003400000076" {
		t.Errorf("unexpected archive start: %s", full.Archive.Start)
	}
	if full.Archive.Stop != "000000010000003400000078" {
		t.Errorf("unexpected archive stop: %s", full.Archive.Stop)
	}
	if full.LSN.Start != "34/76000028" {
		t.Errorf("unexpected LSN start: %s", full.LSN.Start)
	}
	if full.Timestamp.Start != 1742929591 {
		t.Errorf("unexpected timestamp start: %d", full.Timestamp.Start)
	}
	if full.Timestamp.Stop != 1742931337 {
		t.Errorf("unexpected timestamp stop: %d", full.Timestamp.Stop)
	}
	if full.Error {
		t.Error("expected error to be false")
	}

	diff := stanzas[0].Backup[1]
	if diff.Type != "diff" {
		t.Errorf("expected type diff, got %s", diff.Type)
	}
}

func TestLatestBackup(t *testing.T) {
	var stanzas []StanzaInfo
	if err := json.Unmarshal([]byte(sampleInfoJSON), &stanzas); err != nil {
		t.Fatalf("failed to parse: %v", err)
	}

	latest := stanzas[0].LatestBackup()
	if latest == nil {
		t.Error("expected latest backup, got nil")
		return
	}
	if latest.Type != "diff" {
		t.Errorf("expected latest backup to be diff, got %s", latest.Type)
	}
	if latest.Label != "20260325-200017F_20260326-085844D" {
		t.Errorf("unexpected latest label: %s", latest.Label)
	}
}

func TestLatestBackup_Empty(t *testing.T) {
	var stanzas []StanzaInfo
	if err := json.Unmarshal([]byte(sampleInfoEmptyJSON), &stanzas); err != nil {
		t.Fatalf("failed to parse: %v", err)
	}

	latest := stanzas[0].LatestBackup()
	if latest != nil {
		t.Error("expected nil for empty backup list")
	}
}

func TestStanzaInfoRunningStatus(t *testing.T) {
	tests := []struct {
		name       string
		json       string
		expectCode int
		expectHeld bool
		expectSize int64
		expectCplt int64
	}{
		{
			"first backup - no valid backups",
			sampleInfoRunningJSON,
			2, true, 109792819251, 75161927680,
		},
		{
			"subsequent backup - ok status",
			sampleInfoRunningWithBackupsJSON,
			0, true, 109792819251, 50000000000,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stanzas []StanzaInfo
			if err := json.Unmarshal([]byte(tt.json), &stanzas); err != nil {
				t.Fatalf("failed to parse: %v", err)
			}

			stanza := stanzas[0]
			if stanza.Status.Code != tt.expectCode {
				t.Errorf("expected status code %d, got %d", tt.expectCode, stanza.Status.Code)
			}
			if stanza.Status.Lock == nil {
				t.Error("expected lock info, got nil")
				return
			}
			if stanza.Status.Lock.Backup.Held != tt.expectHeld {
				t.Errorf("expected backup held %v, got %v", tt.expectHeld, stanza.Status.Lock.Backup.Held)
			}
			if stanza.Status.Lock.Backup.Size != tt.expectSize {
				t.Errorf("expected size %d, got %d", tt.expectSize, stanza.Status.Lock.Backup.Size)
			}
			if stanza.Status.Lock.Backup.SizeCplt != tt.expectCplt {
				t.Errorf("expected size-cplt %d, got %d", tt.expectCplt, stanza.Status.Lock.Backup.SizeCplt)
			}
		})
	}
}

func TestFindBackupByAnnotation(t *testing.T) {
	tests := []struct {
		name          string
		backups       []BackupInfo
		key           string
		value         string
		expectedLabel string
	}{
		{
			"finds matching backup",
			[]BackupInfo{
				{Label: "20260414-131825F", Annotation: map[string]string{"backup-cr": "backup-1"}},
				{Label: "20260414-132407D", Annotation: map[string]string{"backup-cr": "backup-2"}},
			},
			"backup-cr", "backup-2", "20260414-132407D",
		},
		{
			"finds first backup",
			[]BackupInfo{
				{Label: "20260414-131825F", Annotation: map[string]string{"backup-cr": "backup-1"}},
				{Label: "20260414-132407D", Annotation: map[string]string{"backup-cr": "backup-2"}},
			},
			"backup-cr", "backup-1", "20260414-131825F",
		},
		{
			"not found",
			[]BackupInfo{
				{Label: "20260414-131825F", Annotation: map[string]string{"backup-cr": "backup-1"}},
			},
			"backup-cr", "nonexistent", "",
		},
		{
			"wrong key",
			[]BackupInfo{
				{Label: "20260414-131825F", Annotation: map[string]string{"backup-cr": "backup-1"}},
			},
			"other-key", "backup-1", "",
		},
		{
			"nil annotation map",
			[]BackupInfo{
				{Label: "20260414-131825F"},
			},
			"backup-cr", "backup-1", "",
		},
		{
			"empty backup list",
			[]BackupInfo{},
			"backup-cr", "backup-1", "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stanza := StanzaInfo{Backup: tt.backups}
			found := stanza.FindBackupByAnnotation(tt.key, tt.value)
			if tt.expectedLabel == "" {
				if found != nil {
					t.Errorf("expected nil, got %s", found.Label)
				}
			} else {
				if found == nil || found.Label != tt.expectedLabel {
					t.Errorf("expected %s, got %v", tt.expectedLabel, found)
				}
			}
		})
	}
}

func TestBackupInfoSize(t *testing.T) {
	var stanzas []StanzaInfo
	if err := json.Unmarshal([]byte(sampleInfoJSON), &stanzas); err != nil {
		t.Fatalf("failed to parse: %v", err)
	}

	full := stanzas[0].Backup[0]
	if full.Info.Size != 274463129600 {
		t.Errorf("unexpected size: %d", full.Info.Size)
	}
	if full.Info.Repository.Size != 30064771072 {
		t.Errorf("unexpected repo size: %d", full.Info.Repository.Size)
	}
}
