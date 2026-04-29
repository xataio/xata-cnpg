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

func TestPITRTracker_FirstCall(t *testing.T) {
	tracker := &PITRTracker{}

	if !tracker.RecordArchive("2026-04-28T10:00:00Z") {
		t.Error("should update on first call (lastUpdate is zero)")
	}

	publish := tracker.Update()
	if publish != "" {
		t.Errorf("first call should publish empty, got %s", publish)
	}
	if tracker.candidate != "2026-04-28T10:00:00Z" {
		t.Errorf("candidate should be set, got %s", tracker.candidate)
	}
}

func TestPITRTracker_SecondCall(t *testing.T) {
	tracker := &PITRTracker{
		candidate:        "2026-04-28T10:00:00Z",
		latestArchivedAt: "2026-04-28T10:04:58Z",
		lastUpdate:       time.Now().Add(-6 * time.Minute),
	}

	if !tracker.RecordArchive("2026-04-28T10:05:00Z") {
		t.Error("should update after 6 minutes")
	}

	publish := tracker.Update()
	if publish != "2026-04-28T10:00:00Z" {
		t.Errorf("should publish previous candidate, got %s", publish)
	}
	if tracker.candidate != "2026-04-28T10:05:00Z" {
		t.Errorf("candidate should be latest, got %s", tracker.candidate)
	}
}

func TestPITRTracker_ThrottleNotReached(t *testing.T) {
	tracker := &PITRTracker{
		lastUpdate: time.Now().Add(-2 * time.Minute),
	}

	if tracker.RecordArchive("2026-04-28T10:02:00Z") {
		t.Error("should not update before 5 minutes")
	}
}

func TestPITRTracker_EmptyArchivedAt(t *testing.T) {
	tracker := &PITRTracker{}

	if tracker.RecordArchive("") {
		t.Error("should not update with empty archivedAt")
	}
	if tracker.latestArchivedAt != "" {
		t.Error("empty archivedAt should not be recorded")
	}
}

func TestPITRTracker_IdleThenResume(t *testing.T) {
	// Simulate: active archiving, then idle, then resume
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
