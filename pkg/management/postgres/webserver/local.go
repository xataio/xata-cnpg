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
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/cloudnative-pg/machinery/pkg/log"
	apierrs "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"

	apiv1 "github.com/xataio/xata-cnpg/api/v1"
	"github.com/xataio/xata-cnpg/internal/management/cache"
	"github.com/xataio/xata-cnpg/pkg/management/postgres"
	"github.com/xataio/xata-cnpg/pkg/management/url"
	"github.com/xataio/xata-cnpg/pkg/pgbackrest"
	"github.com/xataio/xata-cnpg/pkg/resources/status"
)

type localWebserverEndpoints struct {
	typedClient   client.Client
	instance      *postgres.Instance
	eventRecorder record.EventRecorder

	// lastPITRUpdate tracks when we last updated LastRecoverabilityPoint
	// on the cluster status. Used to throttle updates to every 5 minutes.
	lastPITRUpdate time.Time
}

// NewLocalWebServer returns a webserver that allows connection only from localhost
func NewLocalWebServer(
	instance *postgres.Instance,
	cli client.Client,
	recorder record.EventRecorder,
) (*Webserver, error) {
	endpoints := localWebserverEndpoints{
		typedClient:   cli,
		instance:      instance,
		eventRecorder: recorder,
	}

	serveMux := http.NewServeMux()
	serveMux.HandleFunc(url.PathCache, endpoints.serveCache)
	serveMux.HandleFunc(url.PathPgBackup, endpoints.requestBackup)
	serveMux.HandleFunc(url.PathWALArchiveStatusCondition, endpoints.setWALArchiveStatusCondition)
	serveMux.HandleFunc(url.PathWALArchiveRecord, endpoints.recordWALArchive)

	server := &http.Server{
		Addr:              fmt.Sprintf("localhost:%d", url.LocalPort),
		Handler:           serveMux,
		ReadHeaderTimeout: DefaultReadTimeout,
		ReadTimeout:       DefaultReadTimeout,
	}

	webserver := NewWebServer(server)

	return webserver, nil
}

// This probe is for the instance status, including replication
func (ws *localWebserverEndpoints) serveCache(w http.ResponseWriter, r *http.Request) {
	requestedObject := strings.TrimPrefix(r.URL.Path, url.PathCache)

	log.Debug("Cached object request received")

	var js []byte
	switch requestedObject {
	case cache.ClusterKey:
		cluster, err := ws.getCluster(r.Context())
		if apierrs.IsNotFound(err) {
			w.WriteHeader(http.StatusNotFound)
			return
		} else if err != nil {
			log.Error(err, "while loading cluster")
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		js, err = json.Marshal(cluster)
		if err != nil {
			log.Error(err, "while marshalling the cluster")
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
	case cache.WALRestoreKey, cache.WALArchiveKey:
		response, err := cache.LoadEnv(requestedObject)
		if errors.Is(err, cache.ErrCacheMiss) {
			w.WriteHeader(http.StatusNotFound)
			return
		} else if err != nil {
			log.Error(err, "while loading cached env")
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		js, err = json.Marshal(response)
		if err != nil {
			log.Error(err, "while marshalling cached env")
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
	default:
		log.Debug("Unsupported cached object type")
		w.WriteHeader(http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(js)
}

// This function schedule a backup
func (ws *localWebserverEndpoints) requestBackup(w http.ResponseWriter, r *http.Request) {
	var backup apiv1.Backup

	ctx := context.Background()

	backupName := r.URL.Query().Get("name")
	if len(backupName) == 0 {
		http.Error(w, "Missing backup name parameter", http.StatusBadRequest)
		return
	}

	cluster, err := ws.getCluster(ctx)
	if err != nil {
		http.Error(
			w,
			fmt.Sprintf("error while getting cluster: %v", err.Error()),
			http.StatusInternalServerError)
		return
	}

	if err := ws.typedClient.Get(ctx, client.ObjectKey{
		Namespace: ws.instance.GetNamespaceName(),
		Name:      backupName,
	}, &backup); err != nil {
		http.Error(
			w,
			fmt.Sprintf("error while getting backup: %v", err.Error()),
			http.StatusInternalServerError)
		return
	}

	switch backup.Spec.Method {
	case apiv1.BackupMethodBarmanObjectStore:
		if cluster.Spec.Backup == nil || cluster.Spec.Backup.BarmanObjectStore == nil {
			http.Error(w, "Barman backup not configured in the cluster", http.StatusConflict)
			return
		}

		if err := ws.startBarmanBackup(ctx, cluster, &backup); err != nil {
			http.Error(
				w,
				fmt.Sprintf("error while requesting backup: %v", err.Error()),
				http.StatusInternalServerError)
			return
		}
		_, _ = fmt.Fprint(w, "OK")

	case apiv1.BackupMethodPlugin:
		if backup.Spec.PluginConfiguration.IsEmpty() {
			http.Error(w, "Plugin backup not configured in the cluster", http.StatusConflict)
			return
		}

		ws.startPluginBackup(ctx, cluster, &backup)
		_, _ = fmt.Fprint(w, "OK")

	case apiv1.BackupMethodPgBackRest:
		if cluster.Spec.Backup == nil || !cluster.Spec.Backup.IsPgBackRestConfigured() {
			http.Error(w, "pgBackRest backup not configured in the cluster", http.StatusConflict)
			return
		}

		if err := ws.startPgBackRestBackup(ctx, cluster, &backup); err != nil {
			http.Error(
				w,
				fmt.Sprintf("error while requesting backup: %v", err.Error()),
				http.StatusInternalServerError)
			return
		}
		_, _ = fmt.Fprint(w, "OK")

	default:
		http.Error(
			w,
			fmt.Sprintf("Unknown backup method: %v", backup.Spec.Method),
			http.StatusBadRequest)
	}
}

func (ws *localWebserverEndpoints) getCluster(ctx context.Context) (*apiv1.Cluster, error) {
	var cluster apiv1.Cluster
	if err := ws.typedClient.Get(ctx, client.ObjectKey{
		Namespace: ws.instance.GetNamespaceName(),
		Name:      ws.instance.GetClusterName(),
	}, &cluster); err != nil {
		return nil, err
	}
	return &cluster, nil
}

func (ws *localWebserverEndpoints) startBarmanBackup(
	ctx context.Context,
	cluster *apiv1.Cluster,
	backup *apiv1.Backup,
) error {
	backupLog := log.WithValues(
		"backupName", backup.Name,
		"backupNamespace", backup.Name)

	backupCommand, err := postgres.NewBarmanBackupCommand(
		cluster,
		backup,
		ws.typedClient,
		ws.eventRecorder,
		ws.instance,
		backupLog,
	)
	if err != nil {
		return fmt.Errorf("while initializing backup: %w", err)
	}

	if err := backupCommand.Start(ctx); err != nil {
		return fmt.Errorf("while starting backup: %w", err)
	}

	return nil
}

func (ws *localWebserverEndpoints) startPluginBackup(
	ctx context.Context,
	cluster *apiv1.Cluster,
	backup *apiv1.Backup,
) {
	NewPluginBackupCommand(cluster, backup, ws.typedClient, ws.eventRecorder).Start(ctx)
}

func (ws *localWebserverEndpoints) startPgBackRestBackup(
	ctx context.Context,
	cluster *apiv1.Cluster,
	backup *apiv1.Backup,
) error {
	backupLog := log.WithValues(
		"backupName", backup.Name,
		"backupNamespace", backup.Namespace)

	backupCommand := postgres.NewPgBackRestBackupCommand(
		cluster,
		backup,
		ws.typedClient,
		ws.eventRecorder,
		ws.instance,
		backupLog,
	)

	if err := backupCommand.Start(ctx); err != nil {
		return fmt.Errorf("while starting backup: %w", err)
	}

	return nil
}

// ArchiveStatusRequest is the request body for the archive status endpoint
type ArchiveStatusRequest struct {
	Error string `json:"error,omitempty"`
}

// WALArchiveRecord is the request body for recording a WAL archive timestamp.
type WALArchiveRecord struct {
	WALName string `json:"walName"`
	ModTime string `json:"modTime"`
}

func (asr *ArchiveStatusRequest) getContinuousArchivingCondition() metav1.Condition {
	if asr.Error != "" {
		return metav1.Condition{
			Type:    string(apiv1.ConditionContinuousArchiving),
			Status:  metav1.ConditionFalse,
			Reason:  string(apiv1.ConditionReasonContinuousArchivingFailing),
			Message: asr.Error,
		}
	}

	return metav1.Condition{
		Type:    string(apiv1.ConditionContinuousArchiving),
		Status:  metav1.ConditionTrue,
		Reason:  string(apiv1.ConditionReasonContinuousArchivingSuccess),
		Message: "Continuous archiving is working",
	}
}

func (ws *localWebserverEndpoints) setWALArchiveStatusCondition(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	contextLogger := log.FromContext(ctx)
	// decode body req
	var asr ArchiveStatusRequest
	if err := json.NewDecoder(r.Body).Decode(&asr); err != nil {
		contextLogger.Error(err, "error while decoding request")
		http.Error(w, fmt.Sprintf("error while decoding request: %v", err.Error()), http.StatusBadRequest)
		return
	}

	cluster, err := ws.getCluster(ctx)
	if err != nil {
		http.Error(
			w,
			fmt.Sprintf("error while getting cluster: %v", err.Error()),
			http.StatusInternalServerError)
		return
	}

	if errCond := status.PatchConditionsWithOptimisticLock(
		ctx,
		ws.typedClient,
		cluster,
		asr.getContinuousArchivingCondition(),
	); errCond != nil {
		contextLogger.Error(errCond, "Error changing wal archiving condition",
			"condition", asr.getContinuousArchivingCondition())
		http.Error(
			w,
			fmt.Sprintf("error while updating wal archiving condition: %v", errCond.Error()),
			http.StatusInternalServerError)
		return
	}

	_, _ = fmt.Fprint(w, "OK")
}

func (ws *localWebserverEndpoints) recordWALArchive(w http.ResponseWriter, r *http.Request) {
	var record WALArchiveRecord
	if err := json.NewDecoder(r.Body).Decode(&record); err != nil {
		http.Error(w, fmt.Sprintf("error decoding request: %v", err), http.StatusBadRequest)
		return
	}

	if record.WALName == "" {
		http.Error(w, "walName is required", http.StatusBadRequest)
		return
	}

	modTime, err := time.Parse(time.RFC3339Nano, record.ModTime)
	if err != nil {
		http.Error(w, fmt.Sprintf("error parsing modTime: %v", err), http.StatusBadRequest)
		return
	}

	if ws.instance.WALCache != nil {
		ws.instance.WALCache.Record(record.WALName, modTime)
	}

	// Throttled PITR update: update LastRecoverabilityPoint at most every 5 minutes.
	// We check if the previous WAL (WAL_N-1) has been confirmed in S3 by looking
	// for its .ok status file in the spool directory. If confirmed, we use
	// WAL_N-1's file modification time as the safe PITR timestamp.
	// 5 minutes = archive_timeout default. If archive_timeout changes, this should too.
	if time.Since(ws.lastPITRUpdate) >= 5*time.Minute && ws.instance.WALCache != nil {
		ws.updateLastRecoverabilityPoint(r.Context(), record.WALName)
	}

	_, _ = fmt.Fprint(w, "OK")
}

// updateLastRecoverabilityPoint checks if the previous WAL has been uploaded
// to S3 by looking for its .ok status file in the pgbackrest spool directory.
// If confirmed, it uses the previous WAL's file modification time (from the
// cache) as the LastRecoverabilityPoint.
func (ws *localWebserverEndpoints) updateLastRecoverabilityPoint(ctx context.Context, currentWAL string) {
	contextLogger := log.FromContext(ctx)

	cache := ws.instance.WALCache
	prevWAL, ok := cache.GetEntryBefore(currentWAL)
	if !ok {
		return
	}

	// Check if the previous WAL has been confirmed in S3 via its .ok status file
	stanza := ws.instance.GetClusterName()
	okFile := fmt.Sprintf("%s/%s/archive-push-async/out/%s.ok",
		pgbackrest.SpoolPath, stanza, prevWAL.WALName)

	if _, err := os.Stat(okFile); err != nil {
		return
	}

	formattedTime := prevWAL.ModTime.UTC().Format(time.RFC3339)

	cluster, err := ws.getCluster(ctx)
	if err != nil {
		contextLogger.Info("PITR update: failed to get cluster", "err", err)
		return
	}

	if cluster.Status.LastRecoverabilityPoint == formattedTime {
		ws.lastPITRUpdate = time.Now()
		return
	}

	cluster.Status.LastRecoverabilityPoint = formattedTime
	if err := ws.typedClient.Status().Update(ctx, cluster); err != nil {
		contextLogger.Info("PITR update: failed to update cluster status", "err", err)
		return
	}

	ws.lastPITRUpdate = time.Now()
}
