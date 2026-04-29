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

package webserver

import (
	"testing"
	"time"
)

func TestPITRTracker_RecordArchive(t *testing.T) {
	tests := []struct {
		name           string
		tracker        PITRTracker
		archivedAt     string
		expectShouldUp bool
		expectLatest   string
	}{
		{
			"first call with value",
			PITRTracker{},
			"2026-04-28T10:00:00Z",
			true,
			"2026-04-28T10:00:00Z",
		},
		{
			"throttle not reached",
			PITRTracker{lastUpdate: time.Now().Add(-2 * time.Minute)},
			"2026-04-28T10:02:00Z",
			false,
			"2026-04-28T10:02:00Z",
		},
		{
			"throttle reached",
			PITRTracker{lastUpdate: time.Now().Add(-6 * time.Minute)},
			"2026-04-28T10:06:00Z",
			true,
			"2026-04-28T10:06:00Z",
		},
		{
			"empty archivedAt",
			PITRTracker{},
			"",
			false,
			"",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tracker := tt.tracker
			got := tracker.RecordArchive(tt.archivedAt)
			if got != tt.expectShouldUp {
				t.Errorf("RecordArchive returned %v, want %v", got, tt.expectShouldUp)
			}
			if tracker.latestArchivedAt != tt.expectLatest {
				t.Errorf("latestArchivedAt = %s, want %s", tracker.latestArchivedAt, tt.expectLatest)
			}
		})
	}
}

func TestPITRTracker_Update(t *testing.T) {
	tests := []struct {
		name            string
		tracker         PITRTracker
		expectPublish   string
		expectCandidate string
	}{
		{
			"first update — no candidate yet",
			PITRTracker{latestArchivedAt: "2026-04-28T10:00:00Z"},
			"",
			"2026-04-28T10:00:00Z",
		},
		{
			"second update — publishes previous candidate",
			PITRTracker{
				candidate:        "2026-04-28T10:00:00Z",
				latestArchivedAt: "2026-04-28T10:05:00Z",
			},
			"2026-04-28T10:00:00Z",
			"2026-04-28T10:05:00Z",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tracker := tt.tracker
			publish := tracker.Update()
			if publish != tt.expectPublish {
				t.Errorf("Update returned %s, want %s", publish, tt.expectPublish)
			}
			if tracker.candidate != tt.expectCandidate {
				t.Errorf("candidate = %s, want %s", tracker.candidate, tt.expectCandidate)
			}
		})
	}
}

func TestPITRTracker_IdleThenResume(t *testing.T) {
	tracker := &PITRTracker{}

	// T=0: First throttle fire
	tracker.RecordArchive("2026-04-28T10:00:00Z")
	tracker.Update()

	// T=1-3min: Active archiving
	tracker.RecordArchive("2026-04-28T10:01:00Z")
	tracker.RecordArchive("2026-04-28T10:02:00Z")
	tracker.RecordArchive("2026-04-28T10:03:00Z")

	// T=3min: Archiving stops (database idle)
	// latestArchivedAt = 10:03:00, candidate = 10:00:00

	// T=7min: A WAL comes in, throttle fires
	tracker.lastUpdate = time.Now().Add(-7 * time.Minute)
	tracker.RecordArchive("2026-04-28T10:07:00Z")

	publish := tracker.Update()
	if publish != "2026-04-28T10:00:00Z" {
		t.Errorf("should publish T=0 candidate, got %s", publish)
	}
	// candidate is set from latestArchivedAt, which was overwritten to 10:07:00
	// when archiving resumed. The 10:03:00 value from the idle period is lost,
	// but that's fine — all WALs up to 10:07:00 are in S3 anyway.
	if tracker.candidate != "2026-04-28T10:07:00Z" {
		t.Errorf("candidate should be latest archived, got %s", tracker.candidate)
	}

	// T=12min: Another throttle fire
	tracker.lastUpdate = time.Now().Add(-6 * time.Minute)
	tracker.RecordArchive("2026-04-28T10:12:00Z")
	publish = tracker.Update()
	if publish != "2026-04-28T10:07:00Z" {
		t.Errorf("should publish 10:07, got %s", publish)
	}
}
