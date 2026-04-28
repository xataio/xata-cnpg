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
	"sync"
	"time"
)

// WALCacheEntry records the file modification time of an archived WAL.
type WALCacheEntry struct {
	WALName string    `json:"wal"`
	ModTime time.Time `json:"time"`
}

// WALCache tracks recently archived WAL files and their modification times.
// Used to determine the last recoverability point by correlating the last
// WAL confirmed in S3 (from pgbackrest info) with its file timestamp.
//
// The wal-archive CLI process sends entries via the local HTTP endpoint,
// and the instance manager reconciler reads them.
type WALCache struct {
	mu      sync.Mutex
	entries []WALCacheEntry
	maxAge  time.Duration
}

// NewWALCache creates a WAL cache that retains entries for the given duration.
func NewWALCache(maxAge time.Duration) *WALCache {
	return &WALCache{
		maxAge: maxAge,
	}
}

// Record adds a WAL entry to the cache and trims expired entries.
func (c *WALCache) Record(walName string, modTime time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.entries = append(c.entries, WALCacheEntry{
		WALName: walName,
		ModTime: modTime,
	})
	c.trim()
}

// GetTimeBefore returns the modification time of the WAL entry immediately
// before the given WAL name. This is the "one WAL before" timestamp that
// is safe to advertise as a PITR target, because the given WAL (which is
// in S3) acts as a safety buffer.
func (c *WALCache) GetTimeBefore(walName string) (time.Time, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	for i, entry := range c.entries {
		if entry.WALName == walName && i > 0 {
			return c.entries[i-1].ModTime, true
		}
	}

	return time.Time{}, false
}

// trim removes entries older than maxAge.
func (c *WALCache) trim() {
	cutoff := time.Now().Add(-c.maxAge)
	i := 0
	for i < len(c.entries) && c.entries[i].ModTime.Before(cutoff) {
		i++
	}
	c.entries = c.entries[i:]
}
