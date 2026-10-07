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
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/xataio/xata-cnpg/pkg/pgbackrest"

	. "github.com/onsi/gomega"
)

// fakeStanzaInfo answers pgbackrest info with a fixed result and counts calls.
type fakeStanzaInfo struct {
	mu     sync.Mutex
	calls  []string
	result *pgbackrest.StanzaInfo
	err    error
}

func (f *fakeStanzaInfo) info(_ context.Context, stanza string) (*pgbackrest.StanzaInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, stanza)
	return f.result, f.err
}

func (f *fakeStanzaInfo) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func stanzaWithBackups(backups ...pgbackrest.BackupInfo) *pgbackrest.StanzaInfo {
	info := &pgbackrest.StanzaInfo{Backup: backups}
	if len(backups) == 0 {
		info.Status.Code = pgbackrest.StanzaStatusNoValidBackups
	}
	return info
}

// newTestTracker returns a tracker whose background checks signal done.
func newTestTracker(f *fakeStanzaInfo) (*recoverabilityTracker, chan struct{}) {
	done := make(chan struct{}, 8)
	t := newRecoverabilityTracker(f.info)
	t.checkFinished = func() { done <- struct{}{} }
	return t, done
}

func waitCheck(t *testing.T, done chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("stanza check did not finish")
	}
}

func TestRecoverabilityClusterWithBackup(t *testing.T) {
	g := NewWithT(t)
	f := &fakeStanzaInfo{}
	tr, _ := newTestTracker(f)
	start := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)

	in := archiveInput{now: start, stanza: "stanza-a", clusterHasBackup: true}
	g.Expect(tr.due(in)).To(BeTrue())
	g.Expect(tr.accept(start.Add(time.Second), start)).To(BeTrue())

	// Throttled for the next 5 minutes.
	in.now = start.Add(4 * time.Minute)
	g.Expect(tr.due(in)).To(BeFalse())
	in.now = start.Add(5 * time.Minute)
	g.Expect(tr.due(in)).To(BeTrue())

	// The Cluster already reports a backup, so the repository is never asked.
	g.Expect(f.callCount()).To(Equal(0))
}

func TestRecoverabilitySuspendedOrFailing(t *testing.T) {
	g := NewWithT(t)
	tr, _ := newTestTracker(&fakeStanzaInfo{})
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)

	g.Expect(tr.due(archiveInput{now: now, suspended: true, clusterHasBackup: true})).To(BeFalse())
	g.Expect(tr.due(archiveInput{now: now, archiveError: "boom", clusterHasBackup: true})).To(BeFalse())
}

// An adopted warm-pool cluster archives into a stanza that already holds
// backups, while its own status reports none.
func TestRecoverabilityAdoptedClusterUsesStanzaBackup(t *testing.T) {
	g := NewWithT(t)
	f := &fakeStanzaInfo{result: stanzaWithBackups(pgbackrest.BackupInfo{Label: "full"})}
	tr, done := newTestTracker(f)
	poolAck := time.Date(2026, 10, 6, 11, 58, 0, 0, time.UTC)
	adopted := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)

	// While in the pool the cluster is suspended.
	g.Expect(tr.due(archiveInput{now: poolAck, suspended: true, stanza: "pool-x"})).To(BeFalse())

	// First archive after adoption: no cached answer yet, a check starts.
	in := archiveInput{now: adopted, stanza: "stanza-a"}
	g.Expect(tr.due(in)).To(BeFalse())
	waitCheck(t, done)
	g.Expect(f.calls).To(Equal([]string{"stanza-a"}))

	// Next archive: the stanza holds a backup.
	in.now = adopted.Add(30 * time.Second)
	g.Expect(tr.due(in)).To(BeTrue())

	// pg_stat_archiver still shows the last suspended-period ack: rejected,
	// and the throttle is not consumed.
	g.Expect(tr.accept(poolAck, in.now)).To(BeFalse())
	g.Expect(tr.due(in)).To(BeTrue())

	// A push after adoption is accepted.
	g.Expect(tr.accept(adopted.Add(20*time.Second), in.now)).To(BeTrue())

	// The positive answer is cached.
	in.now = in.now.Add(10 * time.Minute)
	g.Expect(tr.due(in)).To(BeTrue())
	g.Expect(f.callCount()).To(Equal(1))
}

// A cluster on a fresh stanza has no backup to restore from, so no stamp.
func TestRecoverabilityStanzaWithoutBackup(t *testing.T) {
	g := NewWithT(t)
	f := &fakeStanzaInfo{result: stanzaWithBackups()}
	tr, done := newTestTracker(f)
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)

	in := archiveInput{now: now, stanza: "stanza-new"}
	g.Expect(tr.due(in)).To(BeFalse())
	waitCheck(t, done)

	// Within the check interval the repository is not asked again.
	in.now = now.Add(time.Minute)
	g.Expect(tr.due(in)).To(BeFalse())
	g.Expect(f.callCount()).To(Equal(1))

	// After it, the repository is asked again.
	in.now = now.Add(stanzaBackupCheckInterval)
	g.Expect(tr.due(in)).To(BeFalse())
	waitCheck(t, done)
	g.Expect(f.callCount()).To(Equal(2))

	// Once the Cluster reports its first backup, stamping starts.
	in.clusterHasBackup = true
	g.Expect(tr.due(in)).To(BeTrue())
}

// A cluster without pgBackRest never asks the repository.
func TestRecoverabilityNoStanza(t *testing.T) {
	g := NewWithT(t)
	f := &fakeStanzaInfo{result: stanzaWithBackups(pgbackrest.BackupInfo{Label: "full"})}
	tr, _ := newTestTracker(f)
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)

	g.Expect(tr.due(archiveInput{now: now})).To(BeFalse())
	g.Expect(tr.due(archiveInput{now: now.Add(stanzaBackupCheckInterval)})).To(BeFalse())
	g.Expect(f.callCount()).To(Equal(0))
	g.Expect(tr.due(archiveInput{now: now, clusterHasBackup: true})).To(BeTrue())
}

func TestRecoverabilityInfoError(t *testing.T) {
	g := NewWithT(t)
	f := &fakeStanzaInfo{err: errors.New("object store unreachable")}
	tr, done := newTestTracker(f)
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)

	g.Expect(tr.due(archiveInput{now: now, stanza: "stanza-a"})).To(BeFalse())
	waitCheck(t, done)
	g.Expect(tr.due(archiveInput{now: now.Add(time.Minute), stanza: "stanza-a"})).To(BeFalse())
}

// Suspending again resets the active window, so acks from the new
// suspended period are rejected after the next resume.
func TestRecoverabilityResuspendResetsActiveWindow(t *testing.T) {
	g := NewWithT(t)
	tr, _ := newTestTracker(&fakeStanzaInfo{})
	t0 := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)

	g.Expect(tr.due(archiveInput{now: t0, clusterHasBackup: true})).To(BeTrue())
	g.Expect(tr.due(archiveInput{now: t0.Add(time.Minute), suspended: true})).To(BeFalse())
	g.Expect(tr.accept(t0.Add(time.Minute), t0.Add(time.Minute))).To(BeFalse())

	resumed := t0.Add(10 * time.Minute)
	g.Expect(tr.due(archiveInput{now: resumed, clusterHasBackup: true})).To(BeTrue())
	g.Expect(tr.accept(t0.Add(2*time.Minute), resumed)).To(BeFalse())
	g.Expect(tr.accept(resumed.Add(time.Second), resumed)).To(BeTrue())
}

func TestStanzaHasValidBackup(t *testing.T) {
	g := NewWithT(t)

	g.Expect(stanzaHasValidBackup(nil)).To(BeFalse())
	g.Expect(stanzaHasValidBackup(stanzaWithBackups())).To(BeFalse())
	g.Expect(stanzaHasValidBackup(stanzaWithBackups(pgbackrest.BackupInfo{Error: true}))).To(BeFalse())
	g.Expect(stanzaHasValidBackup(stanzaWithBackups(
		pgbackrest.BackupInfo{Error: true}, pgbackrest.BackupInfo{}))).To(BeTrue())

	missing := stanzaWithBackups(pgbackrest.BackupInfo{})
	missing.Status.Code = pgbackrest.StanzaStatusMissingData
	g.Expect(stanzaHasValidBackup(missing)).To(BeFalse())
}
