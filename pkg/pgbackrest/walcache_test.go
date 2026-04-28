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
	"testing"
	"time"
)

func TestWALCache_GetTimeBefore(t *testing.T) {
	cache := NewWALCache(10 * time.Minute)
	now := time.Now()

	cache.Record("000000010000000000000001", now.Add(-3*time.Minute))
	cache.Record("000000010000000000000002", now.Add(-2*time.Minute))
	cache.Record("000000010000000000000003", now.Add(-1*time.Minute))

	tests := []struct {
		name       string
		walName    string
		expectOK   bool
		expectTime time.Time
	}{
		{
			"returns previous WAL time",
			"000000010000000000000003",
			true,
			now.Add(-2 * time.Minute),
		},
		{
			"returns first WAL time for second entry",
			"000000010000000000000002",
			true,
			now.Add(-3 * time.Minute),
		},
		{
			"no previous for first entry",
			"000000010000000000000001",
			false,
			time.Time{},
		},
		{
			"not found",
			"000000010000000000000099",
			false,
			time.Time{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := cache.GetTimeBefore(tt.walName)
			if ok != tt.expectOK {
				t.Errorf("expected ok=%v, got %v", tt.expectOK, ok)
			}
			if ok && !got.Equal(tt.expectTime) {
				t.Errorf("expected time %v, got %v", tt.expectTime, got)
			}
		})
	}
}

func TestWALCache_Trim(t *testing.T) {
	cache := NewWALCache(1 * time.Second)

	cache.Record("old-wal", time.Now().Add(-2*time.Second))
	cache.Record("new-wal", time.Now())

	// Trigger trim by recording another entry
	cache.Record("newest-wal", time.Now())

	_, ok := cache.GetTimeBefore("new-wal")
	// old-wal should be trimmed, so new-wal has no previous
	if ok {
		t.Error("expected old entry to be trimmed")
	}
}

func TestWALCache_EmptyCache(t *testing.T) {
	cache := NewWALCache(10 * time.Minute)

	_, ok := cache.GetTimeBefore("anything")
	if ok {
		t.Error("expected false for empty cache")
	}
}
