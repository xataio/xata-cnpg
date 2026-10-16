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

package controller

import (
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/metrics"

	apiv1 "github.com/xataio/xata-cnpg/api/v1"
)

// backupAttemptDedupeTTL is how long a Backup UID is remembered after the
// reconciler last saw it. The entry is refreshed on every reconcile, and
// controller-runtime resyncs every object about every ten hours (its
// default SyncPeriod with up to 10% jitter, which the operator does not
// override), so a Backup
// that still exists is never forgotten and never counted twice. Only an
// entry for a deleted Backup stops being refreshed and ages out.
const backupAttemptDedupeTTL = 24 * time.Hour

// backupAttemptsTotal counts terminal Backup reconciliations by outcome, so
// an attempt success ratio can be computed over a window much longer than a
// Backup object's own lifetime (it is usually garbage collected within
// hours). It is registered on controller-runtime's global registry, so it is
// served on the metrics endpoint the operator already exposes.
//
// failure_reason is empty for a completed backup; that is expected, not
// missing data.
var backupAttemptsTotal = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Namespace: "cnpg",
		Subsystem: "backup",
		Name:      "attempts_total",
		Help: "Total number of Backup objects that reached a terminal phase, labeled by outcome. " +
			"Counted once per Backup UID regardless of how many times it is reconciled afterwards.",
	},
	[]string{"phase", "method", "failure_reason"},
)

func init() {
	metrics.Registry.MustRegister(backupAttemptsTotal)
}

// backupAttemptDeduper remembers which terminal Backup UIDs have already
// been counted, so a Backup reconciled repeatedly after reaching a terminal
// phase (which is the normal case: it sits around until the cleaner CronJob
// or its Cluster's garbage collection removes it) is only counted once.
//
// The zero value is ready to use.
//
// Known and accepted imprecision: an operator restart starts this map empty,
// so any terminal Backup still present in the cluster is re-counted once
// after a restart. The present Backups mirror the recent mix of outcomes, so
// this inflates the counts more than it moves the success ratio, and it
// avoids persisting dedupe state somewhere outside the process.
type backupAttemptDeduper struct {
	mu       sync.Mutex
	lastSeen map[types.UID]time.Time

	// now returns the current time. It is nil outside tests.
	now func() time.Time
}

// observeTerminal records that backup reached a terminal phase and reports
// whether this call is the first to do so for its UID. Every call refreshes
// the UID's last-seen time, so the entry ages out backupAttemptDedupeTTL
// after the last reconcile of the Backup, not after the first. Ageing out
// from the first sighting would forget a terminal Backup that outlives the
// TTL (some do, by months) and count it again after the next resync.
func (d *backupAttemptDeduper) observeTerminal(backup *apiv1.Backup) bool {
	d.mu.Lock()
	defer d.mu.Unlock()

	now := time.Now()
	if d.now != nil {
		now = d.now()
	}

	if d.lastSeen == nil {
		d.lastSeen = make(map[types.UID]time.Time)
	}

	d.evictLocked(now)

	_, seen := d.lastSeen[backup.UID]
	d.lastSeen[backup.UID] = now
	return !seen
}

func (d *backupAttemptDeduper) evictLocked(now time.Time) {
	cutoff := now.Add(-backupAttemptDedupeTTL)
	for uid, lastSeen := range d.lastSeen {
		if lastSeen.Before(cutoff) {
			delete(d.lastSeen, uid)
		}
	}
}

// recordBackupAttempt increments backupAttemptsTotal for backup's terminal
// outcome, once per Backup UID.
func (r *BackupReconciler) recordBackupAttempt(backup *apiv1.Backup) {
	if !r.backupAttempts.observeTerminal(backup) {
		return
	}

	backupAttemptsTotal.WithLabelValues(
		string(backup.Status.Phase),
		string(backup.Status.Method),
		string(backup.Status.FailureReason),
	).Inc()
}
