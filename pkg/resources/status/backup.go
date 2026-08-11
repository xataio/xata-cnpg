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

package status

import (
	"context"
	"fmt"
	"time"

	"github.com/cloudnative-pg/machinery/pkg/log"
	pgTime "github.com/cloudnative-pg/machinery/pkg/postgres/time"
	apierrs "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"

	apiv1 "github.com/xataio/xata-cnpg/api/v1"
	"github.com/xataio/xata-cnpg/pkg/utils"
)

// BackupTransaction is a function that modifies a Backup object.
type BackupTransaction func(*apiv1.Backup)

type flagBackupErrors struct {
	clusterStatusErr    error
	backupErr           error
	clusterConditionErr error
}

func (f flagBackupErrors) Error() string {
	var message string
	if f.clusterStatusErr != nil {
		message += fmt.Sprintf("error patching cluster status: %v; ", f.clusterStatusErr)
	}
	if f.backupErr != nil {
		message += fmt.Sprintf("error patching backup status: %v; ", f.backupErr)
	}
	if f.clusterConditionErr != nil {
		message += fmt.Sprintf("error patching cluster conditions: %v; ", f.clusterConditionErr)
	}

	return message
}

// toError returns the errors encountered or nil
func (f flagBackupErrors) toError() error {
	if f.clusterStatusErr != nil || f.backupErr != nil || f.clusterConditionErr != nil {
		return f
	}
	return nil
}

// FlagBackupAsFailed updates the status of a Backup object to indicate that it
// has failed. When the failure is a consequence of the target cluster being
// hibernated or deleted, the backup is flagged as cancelled instead: the
// interruption is the result of a deliberate platform action, not a backup
// problem, so the cluster status (LastFailedBackup, failed condition) is left
// untouched and no alert is raised.
func FlagBackupAsFailed(
	ctx context.Context,
	cli client.Client,
	backup *apiv1.Backup,
	cluster *apiv1.Cluster,
	err error,
	transactions ...BackupTransaction,
) error {
	contextLogger := log.FromContext(ctx)

	if reason, cancel := backupCancellationReason(ctx, cli, backup); cancel {
		contextLogger.Info("Backup interrupted by hibernation or cluster deletion, flagging as cancelled",
			"backupName", backup.Name, "reason", reason)
		return flagBackupAsCancelled(ctx, cli, backup, reason, err, transactions...)
	}

	var flagErr flagBackupErrors

	backupGone := false
	if err := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		var livingBackup apiv1.Backup
		if err := cli.Get(ctx, client.ObjectKeyFromObject(backup), &livingBackup); err != nil {
			// The backup has been deleted: there is nothing left to flag,
			// and no reason to record a failure for a vanished backup.
			if apierrs.IsNotFound(err) {
				backupGone = true
				return nil
			}
			contextLogger.Error(err, "failed to get backup")
			return err
		}
		origBackup := livingBackup.DeepCopy()
		livingBackup.Status.SetAsFailed(err)
		livingBackup.Status.Method = livingBackup.Spec.Method
		for _, transaction := range transactions {
			transaction(&livingBackup)
		}

		err := cli.Status().Patch(ctx, &livingBackup, client.MergeFrom(origBackup))
		if err != nil {
			contextLogger.Error(err, "while patching backup status")
			return err
		}
		// we mutate the original object
		backup.Status = livingBackup.Status

		return nil
	}); err != nil {
		contextLogger.Error(err, "while flagging backup as failed")
		flagErr.backupErr = err
	}

	if backupGone {
		return nil
	}

	if cluster == nil {
		return flagErr.toError()
	}

	if err := PatchWithOptimisticLock(
		ctx,
		cli,
		cluster,
		func(cluster *apiv1.Cluster) {
			cluster.Status.LastFailedBackup = pgTime.GetCurrentTimestampWithFormat(time.RFC3339) //nolint:staticcheck
		},
	); err != nil {
		if !apierrs.IsNotFound(err) {
			contextLogger.Error(err, "while patching cluster status with last failed backup")
			flagErr.clusterStatusErr = err
		}
	}

	if err := PatchConditionsWithOptimisticLock(
		ctx,
		cli,
		cluster,
		apiv1.BuildClusterBackupFailedCondition(err),
	); err != nil {
		if !apierrs.IsNotFound(err) {
			contextLogger.Error(err, "while patching backup condition in the cluster status (backup failed)")
			flagErr.clusterConditionErr = err
		}
	}

	return flagErr.toError()
}

// backupCancellationReason inspects the live state of the backup's target
// cluster and returns the reason the backup should be flagged as cancelled
// instead of failed: the cluster is gone, being deleted, or hibernated. The
// cluster is fetched fresh rather than taken from the caller because the
// caller may hold a stale copy that predates the hibernation annotation
// (notably the instance manager, whose backup fails as a consequence of
// hibernation shutting the pod down).
func backupCancellationReason(
	ctx context.Context,
	cli client.Client,
	backup *apiv1.Backup,
) (string, bool) {
	var cluster apiv1.Cluster
	err := cli.Get(ctx, client.ObjectKey{
		Namespace: backup.Namespace,
		Name:      backup.Spec.Cluster.Name,
	}, &cluster)
	switch {
	case apierrs.IsNotFound(err), apierrs.IsForbidden(err):
		return "cluster has been deleted", true
	case err != nil:
		// we cannot assess the cluster state, proceed with the failure path
		return "", false
	case !cluster.DeletionTimestamp.IsZero():
		return "cluster is being deleted", true
	case cluster.Annotations[utils.HibernationAnnotationName] == string(utils.HibernationAnnotationValueOn):
		return "cluster is hibernated", true
	default:
		return "", false
	}
}

// flagBackupAsCancelled marks the backup as cancelled, recording the reason
// and the error that interrupted it. The cluster status is deliberately not
// updated: a cancelled backup is not a failure.
func flagBackupAsCancelled(
	ctx context.Context,
	cli client.Client,
	backup *apiv1.Backup,
	reason string,
	cause error,
	transactions ...BackupTransaction,
) error {
	contextLogger := log.FromContext(ctx)

	message := reason
	if cause != nil {
		message = fmt.Sprintf("%s (interrupted: %v)", reason, cause)
	}

	if err := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		var livingBackup apiv1.Backup
		if err := cli.Get(ctx, client.ObjectKeyFromObject(backup), &livingBackup); err != nil {
			// The backup has been deleted: there is nothing left to cancel.
			if apierrs.IsNotFound(err) {
				return nil
			}
			return err
		}
		origBackup := livingBackup.DeepCopy()
		livingBackup.Status.SetAsCancelled(message)
		livingBackup.Status.Method = livingBackup.Spec.Method
		for _, transaction := range transactions {
			transaction(&livingBackup)
		}

		if err := cli.Status().Patch(ctx, &livingBackup, client.MergeFrom(origBackup)); err != nil {
			return err
		}
		// we mutate the original object
		backup.Status = livingBackup.Status

		return nil
	}); err != nil {
		contextLogger.Error(err, "while flagging backup as cancelled")
		return err
	}

	return nil
}
