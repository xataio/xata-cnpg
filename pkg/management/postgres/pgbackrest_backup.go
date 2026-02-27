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

	"github.com/cloudnative-pg/machinery/pkg/log"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"

	apiv1 "github.com/xataio/xata-cnpg/api/v1"
	"github.com/xataio/xata-cnpg/pkg/pgbackrest"
	"github.com/xataio/xata-cnpg/pkg/resources"
	"github.com/xataio/xata-cnpg/pkg/resources/status"
)

// PgBackRestBackupCommand represents a pgbackrest backup being executed.
type PgBackRestBackupCommand struct {
	Cluster  *apiv1.Cluster
	Backup   *apiv1.Backup
	Client   client.Client
	Recorder record.EventRecorder
	Log      log.Logger
	Instance *Instance
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
	))

	b.Recorder.Event(b.Backup, "Normal", "Starting", "Backup started")

	// Update cluster condition
	if err := b.retryWithRefreshedCluster(ctx, func() error {
		return status.PatchConditionsWithOptimisticLock(ctx, b.Client, b.Cluster, apiv1.BackupStartingCondition)
	}); err != nil {
		b.Log.Error(err, "Error changing backup condition (backup started)")
	}

	if err := pgbackrest.Backup(ctx, b.Cluster.Name, "full"); err != nil {
		b.Log.Error(err, "Backup failed")
		b.Recorder.Event(b.Backup, "Normal", "Failed", "Backup failed")
		_ = status.FlagBackupAsFailed(ctx, b.Client, b.Backup, b.Cluster, err)
		return
	}

	b.Log.Info("Backup completed")
	b.Recorder.Event(b.Backup, "Normal", "Completed", "Backup completed")

	b.Backup.Status.SetAsCompleted()

	if err := PatchBackupStatusAndRetry(ctx, b.Client, b.Backup); err != nil {
		b.Log.Error(err, "Can't set backup status as completed")
	}

	if err := b.retryWithRefreshedCluster(ctx, func() error {
		return status.PatchConditionsWithOptimisticLock(ctx, b.Client, b.Cluster, apiv1.BackupSucceededCondition)
	}); err != nil {
		b.Log.Error(err, "Can't update the cluster with the completed backup data")
	}
}

func (b *PgBackRestBackupCommand) retryWithRefreshedCluster(
	ctx context.Context,
	cb func() error,
) error {
	return resources.RetryWithRefreshedResource(ctx, b.Client, b.Cluster, cb)
}
