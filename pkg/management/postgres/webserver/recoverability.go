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
	"sync"
	"time"

	"github.com/cloudnative-pg/machinery/pkg/log"

	"github.com/xataio/xata-cnpg/pkg/pgbackrest"
)

const (
	// recoverabilityPointInterval is how often LastRecoverabilityPoint is
	// stamped. It matches the default archive_timeout. If archive_timeout
	// changes, this should too.
	recoverabilityPointInterval = 5 * time.Minute

	// stanzaBackupCheckInterval is how often the repository is asked again
	// whether the stanza holds a backup, while it does not.
	stanzaBackupCheckInterval = 5 * time.Minute

	// stanzaBackupCheckTimeout bounds one pgbackrest info call.
	stanzaBackupCheckTimeout = 2 * time.Minute
)

// stanzaInfoFunc returns the pgbackrest info of a stanza.
type stanzaInfoFunc func(ctx context.Context, stanza string) (*pgbackrest.StanzaInfo, error)

// recoverabilityTracker decides when LastRecoverabilityPoint can be stamped.
//
// A point in time is recoverable only when the stanza holds a base backup
// and the archived WAL reached the repository. The base backup is a fact
// about the stanza, not about the Cluster: a Cluster that takes over an
// existing stanza (a warm-pool wake-up) archives into a stanza that already
// holds backups, while its own status has no LastSuccessfulBackup until its
// first scheduled backup. So when the Cluster reports no backup, the tracker
// asks the repository.
//
// While pgBackRest is suspended, the archive wrapper acknowledges WAL
// without a push, and pg_stat_archiver records those acknowledgements. The
// tracker records when it first saw archiving active, and rejects an
// archive time older than that.
type recoverabilityTracker struct {
	mu sync.Mutex

	// activeSince is when the tracker first saw the cluster not suspended.
	// It is zero while suspended.
	activeSince time.Time
	// lastUpdate is when LastRecoverabilityPoint was last stamped.
	lastUpdate time.Time

	// The stanza backup check. found is sticky for the stanza it was
	// found for.
	info          stanzaInfoFunc
	stanza        string
	found         bool
	checking      bool
	lastCheck     time.Time
	checkFinished func() // test hook, called after each check
}

func newRecoverabilityTracker(info stanzaInfoFunc) *recoverabilityTracker {
	return &recoverabilityTracker{info: info}
}

// archiveInput is what the archive status handler knows about one call.
// stanza is empty when the cluster does not use pgBackRest.
type archiveInput struct {
	now              time.Time
	suspended        bool
	archiveError     string
	stanza           string
	clusterHasBackup bool
}

// due reports whether the handler should read pg_stat_archiver now. It
// records whether archiving is active, and starts a background stanza
// check when the Cluster reports no backup.
func (t *recoverabilityTracker) due(in archiveInput) bool {
	t.mu.Lock()
	defer t.mu.Unlock()

	if in.suspended {
		t.activeSince = time.Time{}
		return false
	}
	if t.activeSince.IsZero() {
		t.activeSince = in.now
	}

	if in.archiveError != "" || in.now.Sub(t.lastUpdate) < recoverabilityPointInterval {
		return false
	}

	if in.clusterHasBackup {
		return true
	}
	return t.stanzaHasBackupLocked(in.stanza, in.now)
}

// accept reports whether an archive time read from pg_stat_archiver can be
// stamped, and records the stamp if so.
func (t *recoverabilityTracker) accept(archived, now time.Time) bool {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.activeSince.IsZero() || archived.Before(t.activeSince) {
		return false
	}
	t.lastUpdate = now
	return true
}

// stanzaHasBackupLocked returns the cached result for the stanza, and starts
// a background check when none is cached and none ran recently. The check
// runs outside archive_command, so a slow object store does not delay WAL
// archiving.
func (t *recoverabilityTracker) stanzaHasBackupLocked(stanza string, now time.Time) bool {
	if stanza == "" {
		return false
	}
	if stanza != t.stanza {
		t.stanza = stanza
		t.found = false
		t.lastCheck = time.Time{}
	}
	if t.found {
		return true
	}
	if t.checking || (!t.lastCheck.IsZero() && now.Sub(t.lastCheck) < stanzaBackupCheckInterval) {
		return false
	}

	t.checking = true
	t.lastCheck = now
	go t.checkStanza(stanza)
	return false
}

func (t *recoverabilityTracker) checkStanza(stanza string) {
	ctx, cancel := context.WithTimeout(context.Background(), stanzaBackupCheckTimeout)
	defer cancel()

	info, err := t.info(ctx, stanza)
	found := err == nil && stanzaHasValidBackup(info)
	if err != nil {
		log.Warning("Cannot read pgbackrest info to decide on the recoverability point",
			"stanza", stanza, "error", err)
	}

	t.mu.Lock()
	if t.stanza == stanza {
		t.found = found
	}
	t.checking = false
	hook := t.checkFinished
	t.mu.Unlock()

	if hook != nil {
		hook()
	}
}

// stanzaHasValidBackup reports whether the stanza holds a backup that a
// restore can start from.
func stanzaHasValidBackup(info *pgbackrest.StanzaInfo) bool {
	if info == nil || info.Status.Code != pgbackrest.StanzaStatusOK {
		return false
	}
	for i := range info.Backup {
		if !info.Backup[i].Error {
			return true
		}
	}
	return false
}
