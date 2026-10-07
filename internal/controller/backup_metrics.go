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

// backupAttemptDedupeTTL bounds how long a Backup UID is remembered for
// dedupe purposes. The cleaner CronJob removes terminal Backups within two
// hours, and BackupOwnerReference often collects them with their Cluster
// sooner than that, so a UID observed longer ago than this TTL belongs to an
// object that is gone (or is about to be) and can safely be forgotten even
// if, improbably, it is still being reconciled.
const backupAttemptDedupeTTL = 24 * time.Hour

// backupAttemptsTotal counts terminal Backup reconciliations by outcome, so
// an attempt success ratio can be computed over a window much longer than a
// Backup object's own lifetime (it is garbage collected within hours). It is
// registered on controller-runtime's global registry, so it is served on the
// metrics endpoint the operator already exposes.
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
// after a restart. This is rare relative to the volume of attempts a ratio
// is computed over, and avoids the complexity of persisting dedupe state
// somewhere outside the process.
type backupAttemptDeduper struct {
	mu      sync.Mutex
	counted map[types.UID]time.Time
}

// observeTerminal records that backup reached a terminal phase and reports
// whether this call is the first to do so for its UID. An entry ages out
// backupAttemptDedupeTTL after it was first counted; that is deliberately
// the observation time, not a field read off the object (e.g. its creation
// timestamp), so the bound does not depend on any timestamp the object
// happens to carry being populated.
func (d *backupAttemptDeduper) observeTerminal(backup *apiv1.Backup) bool {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.counted == nil {
		d.counted = make(map[types.UID]time.Time)
	}

	d.evictLocked()

	if _, ok := d.counted[backup.UID]; ok {
		return false
	}
	d.counted[backup.UID] = time.Now()
	return true
}

func (d *backupAttemptDeduper) evictLocked() {
	cutoff := time.Now().Add(-backupAttemptDedupeTTL)
	for uid, createdAt := range d.counted {
		if createdAt.Before(cutoff) {
			delete(d.counted, uid)
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
