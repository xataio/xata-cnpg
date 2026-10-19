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
	"context"

	"github.com/prometheus/client_golang/prometheus"
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	apiv1 "github.com/xataio/xata-cnpg/api/v1"
)

// backupAttemptsTotal counts Backups that reached a terminal phase, by
// outcome, so an attempt success ratio can be computed over a window much
// longer than a Backup object's own lifetime (it is usually garbage
// collected within hours). It is registered on controller-runtime's global
// registry, so it is served on the metrics endpoint the operator already
// exposes.
//
// failure_reason is empty for a completed backup; that is expected, not
// missing data.
var backupAttemptsTotal = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Namespace: "cnpg",
		Subsystem: "backup",
		Name:      "attempts_total",
		Help:      "Total number of observed Backup transitions into a terminal phase, labeled by outcome.",
	},
	[]string{"phase", "method", "failure_reason"},
)

func init() {
	metrics.Registry.MustRegister(backupAttemptsTotal)
}

// backupAttemptCounter is a Backup event handler that increments
// backupAttemptsTotal when a Backup moves into a terminal phase. It
// enqueues nothing: the reconciler gets its requests from its own watch.
//
// Only an update event whose old object is not terminal and whose new object
// is terminal counts, so each Backup counts once however long it lives. Later
// events for a terminal Backup, such as a resync, do not count. After an
// operator restart the informer replays the existing Backups as create
// events, which do not count.
//
// Known and accepted imprecision: a Backup that reaches a terminal phase
// while the operator is not running is first seen as a create event and is
// never counted. The instance manager writes the terminal phase itself, so
// this can happen during an operator rollout.
var backupAttemptCounter = handler.Funcs{UpdateFunc: countBackupAttempt}

func countBackupAttempt(
	_ context.Context,
	e event.UpdateEvent,
	_ workqueue.TypedRateLimitingInterface[reconcile.Request],
) {
	oldBackup, ok := e.ObjectOld.(*apiv1.Backup)
	if !ok {
		return
	}
	newBackup, ok := e.ObjectNew.(*apiv1.Backup)
	if !ok {
		return
	}

	// A different UID means the Backup was deleted and created again under
	// the same name between two observations, so the old status says nothing
	// about the new object.
	if oldBackup.UID == newBackup.UID && oldBackup.Status.IsDone() {
		return
	}
	if !newBackup.Status.IsDone() {
		return
	}

	backupAttemptsTotal.WithLabelValues(
		string(newBackup.Status.Phase),
		string(newBackup.Status.Method),
		string(newBackup.Status.FailureReason),
	).Inc()
}
