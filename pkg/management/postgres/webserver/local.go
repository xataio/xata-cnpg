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
	"github.com/xataio/xata-cnpg/pkg/resources/status"
)

type localWebserverEndpoints struct {
	typedClient   client.Client
	instance      *postgres.Instance
	eventRecorder record.EventRecorder

	// lastPITRUpdate tracks when we last updated LastRecoverabilityPoint
	// on the cluster status. Used to throttle updates to every 5 minutes.
	lastPITRUpdate time.Time
	// latestArchivedAt is updated on every successful WAL archive.
	latestArchivedAt string
	// pitrCandidate is captured from latestArchivedAt at each throttle fire.
	// Published as LastRecoverabilityPoint on the NEXT throttle fire, after
	// a full interval of buffer WALs has accumulated in S3.
	pitrCandidate string
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
	// ArchivedAt is the wall clock time when the WAL was archived.
	// Used to track the last recoverability point for PITR.
	ArchivedAt string `json:"archivedAt,omitempty"`
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

	// Track the latest archive timestamp on every successful archive.
	if asr.ArchivedAt != "" {
		ws.latestArchivedAt = asr.ArchivedAt
	}

	// Throttled PITR update: every 5 minutes, publish the pitrCandidate
	// (captured at the PREVIOUS throttle point) as LastRecoverabilityPoint,
	// then snapshot latestArchivedAt as the next candidate.
	//
	// This gives the candidate a full throttle interval (~5 min) of buffer
	// WALs in S3 before it's advertised, making it safe for PITR.
	// 5 minutes = archive_timeout default. If archive_timeout changes, this should too.
	if asr.ArchivedAt != "" && time.Since(ws.lastPITRUpdate) >= 5*time.Minute {
		ws.updateLastRecoverabilityPoint(ctx, cluster)
	}

	_, _ = fmt.Fprint(w, "OK")
}

func (ws *localWebserverEndpoints) updateLastRecoverabilityPoint(
	ctx context.Context,
	cluster *apiv1.Cluster,
) {
	if ws.pitrCandidate != "" && ws.pitrCandidate != cluster.Status.LastRecoverabilityPoint {
		cluster.Status.LastRecoverabilityPoint = ws.pitrCandidate
		if err := ws.typedClient.Status().Update(ctx, cluster); err != nil {
			log.Info("PITR update: failed to update cluster status", "err", err)
			return
		}
	}

	ws.pitrCandidate = ws.latestArchivedAt
	ws.lastPITRUpdate = time.Now()
}
