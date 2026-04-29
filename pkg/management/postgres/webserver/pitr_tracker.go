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

import "time"

const pitrThrottleInterval = 5 * time.Minute

// PITRTracker tracks WAL archive timestamps and determines when
// LastRecoverabilityPoint should be updated. It uses a two-phase
// approach: a timestamp is first captured as a candidate, then
// published after a full throttle interval has passed, ensuring
// enough buffer WALs exist in S3 for safe PITR.
type PITRTracker struct {
	lastUpdate       time.Time
	latestArchivedAt string
	candidate        string
}

// RecordArchive records the timestamp of a successful WAL archive
// and returns true if a PITR update should be performed.
func (t *PITRTracker) RecordArchive(archivedAt string) bool {
	if archivedAt == "" {
		return false
	}
	t.latestArchivedAt = archivedAt
	return time.Since(t.lastUpdate) >= pitrThrottleInterval
}

// Update rotates the candidate: returns the current candidate to
// publish (may be empty on first call), then snapshots latestArchivedAt
// as the next candidate.
func (t *PITRTracker) Update() (publish string) {
	publish = t.candidate
	t.candidate = t.latestArchivedAt
	t.lastUpdate = time.Now()
	return publish
}
