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

package postgres

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/cloudnative-pg/machinery/pkg/log"
	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"

	apiv1 "github.com/xataio/xata-cnpg/api/v1"
	"github.com/xataio/xata-cnpg/pkg/pgbackrest"
	"github.com/xataio/xata-cnpg/pkg/resources"
	"github.com/xataio/xata-cnpg/pkg/resources/status"
)

// AnnotationKeyBackupCR is the pgbackrest annotation key used to link a
// backup in the repository to the Kubernetes Backup CR that triggered it.
const AnnotationKeyBackupCR = "backup-cr"

// PgBackRestBackupCommand represents a pgbackrest backup being executed.
type PgBackRestBackupCommand struct {
	Cluster  *apiv1.Cluster
	Backup   *apiv1.Backup
	Client   client.Client
	Recorder record.EventRecorder
	Log      log.Logger
	Instance *Instance

	// statusMu protects writes to Backup.Status from concurrent goroutines
	// (progress ticker and main backup goroutine).
	statusMu sync.Mutex
}

// NewPgBackRestBackupCommand initializes a PgBackRestBackupCommand.
func NewPgBackRestBackupCommand(
	cluster *apiv1.Cluster,
	backup *apiv1.Backup,
	client client.Client,
	recorder record.EventRecorder,
	instance *Instance,
	log log.Logger,
) *PgBackRestBackupCommand {
	return &PgBackRestBackupCommand{
		Cluster:  cluster,
		Backup:   backup,
		Client:   client,
		Recorder: recorder,
		Instance: instance,
		Log:      log,
	}
}

// Start initiates a pgbackrest full backup. It sets the backup status,
// checks WAL archiving is working, and spawns the backup in a goroutine.
func (b *PgBackRestBackupCommand) Start(ctx context.Context) error {
	backupStatus := b.Backup.GetStatus()
	backupStatus.ServerName = b.Cluster.Name
	backupStatus.Phase = apiv1.BackupPhaseRunning
	backupStatus.Method = apiv1.BackupMethodPgBackRest

	if err := PatchBackupStatusAndRetry(ctx, b.Client, b.Backup); err != nil {
		return fmt.Errorf("can't set backup as running: %v", err)
	}

	if err := ensureWalArchiveIsWorking(b.Instance); err != nil {
		b.Log.Warning("WAL archiving is not working", "err", err)
		b.Backup.GetStatus().Phase = apiv1.BackupPhaseWalArchivingFailing
		return PatchBackupStatusAndRetry(ctx, b.Client, b.Backup)
	}

	if b.Backup.GetStatus().Phase != apiv1.BackupPhaseRunning {
		b.Backup.GetStatus().Phase = apiv1.BackupPhaseRunning
		if err := PatchBackupStatusAndRetry(ctx, b.Client, b.Backup); err != nil {
			b.Log.Error(err, "can't set backup as running after WAL archive check")
		}
	}

	go b.run(ctx)

	return nil
}

// run executes the pgbackrest backup command and updates the status.
// This method runs in a dedicated goroutine.
func (b *PgBackRestBackupCommand) run(ctx context.Context) {
	ctx = log.IntoContext(ctx, log.FromContext(ctx).WithValues(
		"backupName", b.Backup.Name,
		"backupNamespace", b.Backup.Namespace,
		"cluster", b.Cluster.Name,
		"backupType", string(b.Backup.Spec.PgBackRestBackupType),
	))

	b.Recorder.Event(b.Backup, "Normal", "Starting", "Backup started")

	// Update cluster condition
	if err := b.retryWithRefreshedCluster(ctx, func() error {
		return status.PatchConditionsWithOptimisticLock(ctx, b.Client, b.Cluster, apiv1.BackupStartingCondition)
	}); err != nil {
		b.Log.Error(err, "Error changing backup condition (backup started)")
	}

	backupType := b.Backup.Spec.PgBackRestBackupType
	if backupType == "" {
		backupType = apiv1.PgBackRestBackupTypeFull
	}

	// Annotate the backup with our CR name so we can find it later
	annotation := fmt.Sprintf("%s=%s", AnnotationKeyBackupCR, b.Backup.Name)

	// Start a progress ticker that polls pgbackrest info during the backup
	progressCtx, stopProgress := context.WithCancel(ctx)
	go b.pollProgress(progressCtx)

	err := pgbackrest.Backup(ctx, b.Cluster.GetPgBackRestStanzaName(), string(backupType), annotation)
	stopProgress()

	if err != nil {
		b.Log.Error(err, "Backup failed")
		b.Recorder.Event(b.Backup, "Normal", "Failed", "Backup failed")
		_ = status.FlagBackupAsFailed(ctx, b.Client, b.Backup, b.Cluster, err)
		return
	}

	b.Log.Info("Backup completed, fetching backup info")

	// Fetch backup details using annotation to find the exact backup
	b.populateBackupDetails(ctx)

	b.Recorder.Event(b.Backup, "Normal", "Completed", "Backup completed")

	b.statusMu.Lock()
	b.Backup.Status.Progress = "100%"
	b.Backup.Status.SetAsCompleted()
	b.statusMu.Unlock()

	if err := PatchBackupStatusAndRetry(ctx, b.Client, b.Backup); err != nil {
		b.Log.Error(err, "Can't set backup status as completed")
	}

	if err := b.retryWithRefreshedCluster(ctx, func() error {
		return status.PatchConditionsWithOptimisticLock(ctx, b.Client, b.Cluster, apiv1.BackupSucceededCondition)
	}); err != nil {
		b.Log.Error(err, "Can't update the cluster with the completed backup data")
	}
}

// pollProgress periodically checks pgbackrest info for backup progress
// and patches the Backup CR status. Runs until ctx is cancelled.
func (b *PgBackRestBackupCommand) pollProgress(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			stanza, err := pgbackrest.Info(ctx, b.Cluster.GetPgBackRestStanzaName())
			if err != nil {
				b.Log.Info("Progress poll: pgbackrest info failed", "err", err)
				continue
			}

			lock := stanza.Status.Lock
			if lock != nil && lock.Backup.Held && lock.Backup.Size > 0 {
				pct := float64(lock.Backup.SizeCplt) / float64(lock.Backup.Size) * 100
				progress := fmt.Sprintf("%.2f%%", pct)
				b.Log.Info("Backup progress", "progress", progress)

				b.statusMu.Lock()
				b.Backup.Status.Progress = progress
				if err := PatchBackupStatusAndRetry(ctx, b.Client, b.Backup); err != nil {
					b.Log.Info("Failed to patch backup progress", "err", err)
				}
				b.statusMu.Unlock()
			}
		}
	}
}

// populateBackupDetails fetches backup details from pgbackrest info,
// matching by the backup-cr annotation. Retries up to 3 times on failure.
// Falls back to the latest backup if annotation matching fails.
func (b *PgBackRestBackupCommand) populateBackupDetails(ctx context.Context) {
	var stanza *pgbackrest.StanzaInfo
	var err error

	for attempt := 1; attempt <= 3; attempt++ {
		stanza, err = pgbackrest.Info(ctx, b.Cluster.GetPgBackRestStanzaName())
		if err == nil {
			break
		}
		b.Log.Warning("Failed to get pgbackrest info, retrying",
			"attempt", attempt, "err", err)
		time.Sleep(time.Duration(attempt) * 2 * time.Second)
	}

	if err != nil {
		b.Log.Error(err, fmt.Sprintf("BACKUP_STATUS_INCOMPLETE: pgbackrest info failed after retries. "+
			"The backup data is in S3 with annotation %s=%s belonging to cluster %s",
			AnnotationKeyBackupCR,
			b.Backup.Name, b.Cluster.Name))
		return
	}

	// Find the backup by annotation, fall back to latest
	backup := stanza.FindBackupByAnnotation(AnnotationKeyBackupCR, b.Backup.Name)
	if backup == nil {
		b.Log.Warning("Backup not found by annotation, falling back to latest")
		backup = stanza.LatestBackup()
	}

	if backup == nil {
		b.Log.Warning("No backup found in pgbackrest info")
		return
	}

	b.statusMu.Lock()
	b.Backup.Status.BackupID = backup.Label
	b.Backup.Status.BackupName = backup.Label
	b.Backup.Status.BeginWal = backup.Archive.Start
	b.Backup.Status.EndWal = backup.Archive.Stop
	b.Backup.Status.BeginLSN = backup.LSN.Start
	b.Backup.Status.EndLSN = backup.LSN.Stop
	b.Backup.Status.StartedAt = &metav1.Time{Time: time.Unix(backup.Timestamp.Start, 0)}
	b.Backup.Status.StoppedAt = &metav1.Time{Time: time.Unix(backup.Timestamp.Stop, 0)}
	b.statusMu.Unlock()

	b.Log.Info("Backup details populated",
		"backupID", backup.Label,
		"beginWal", backup.Archive.Start,
		"endWal", backup.Archive.Stop,
		"beginLSN", backup.LSN.Start,
		"endLSN", backup.LSN.Stop,
	)

	// Update FirstRecoverabilityPoint and LastSuccessfulBackup on the cluster
	var firstRecoverability, lastSuccessful *time.Time
	if first := stanza.Backup[0]; first.Timestamp.Start > 0 {
		firstRecoverability = new(time.Unix(first.Timestamp.Start, 0))
	}
	if last := stanza.LatestBackup(); last != nil && last.Timestamp.Stop > 0 {
		lastSuccessful = new(time.Unix(last.Timestamp.Stop, 0))
	}

	if err = b.retryWithRefreshedCluster(ctx, func() error {
		origCluster := b.Cluster.DeepCopy()
		b.Cluster.UpdateBackupTimes(
			apiv1.BackupMethodPgBackRest,
			firstRecoverability,
			lastSuccessful,
		)
		if equality.Semantic.DeepEqual(origCluster.Status, b.Cluster.Status) {
			return nil
		}
		return b.Client.Status().Patch(ctx, b.Cluster, client.MergeFrom(origCluster))
	}); err != nil {
		b.Log.Error(err, "while setting firstRecoverabilityPoint and lastSuccessfulBackup")
	}
}

func (b *PgBackRestBackupCommand) retryWithRefreshedCluster(
	ctx context.Context,
	cb func() error,
) error {
	return resources.RetryWithRefreshedResource(ctx, b.Client, b.Cluster, cb)
}
