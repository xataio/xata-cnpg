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
	"os"
	"path/filepath"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	apiv1 "github.com/xataio/xata-cnpg/api/v1"
	"github.com/xataio/xata-cnpg/pkg/management/postgres"
	"github.com/xataio/xata-cnpg/pkg/pgbackrest"
	"github.com/xataio/xata-cnpg/pkg/utils"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("pgbackrest stanzaCreateNeeded", func() {
	It("returns true when no stanza has been created yet", func() {
		Expect(stanzaCreateNeeded(nil, "branch-abc")).To(BeTrue())
	})

	It("returns false when the same stanza was already created", func() {
		Expect(stanzaCreateNeeded(ptr.To("branch-abc"), "branch-abc")).To(BeFalse())
	})

	It("returns true when the stanza name changed (e.g. pool cluster adopted by a branch)", func() {
		// Pool cluster created its own (cluster-name) stanza; on adoption the
		// stanza switches to the branch id and must be created, not skipped.
		Expect(stanzaCreateNeeded(ptr.To("pool-cluster-xyz"), "branch-abc")).To(BeTrue())
	})
})

func TestStanzaInitializationAfterConfigChange(t *testing.T) {
	dir := t.TempDir()
	calls := filepath.Join(dir, "calls")
	fail := filepath.Join(dir, "fail")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("STANZA_CALLS", calls)
	t.Setenv("STANZA_FAIL", fail)
	binary := `#!/bin/sh
printf '%s\n' "$*" >> "$STANZA_CALLS"
if [ -f "$STANZA_FAIL" ]; then exit 1; fi
`
	// #nosec G306 -- The fake executable needs owner execute permission inside t.TempDir.
	if err := os.WriteFile(filepath.Join(dir, "pgbackrest"), []byte(binary), 0o700); err != nil {
		t.Fatal(err)
	}
	cluster := &apiv1.Cluster{
		ObjectMeta: metav1.ObjectMeta{Name: "branch"},
		Status:     apiv1.ClusterStatus{CurrentPrimary: "branch-1"},
	}
	r := &InstanceReconciler{instance: postgres.NewInstance().WithPodName("branch-1")}
	r.pgBackRestStanzaCreated.Store(ptr.To("branch"))

	// An unchanged configuration does not initialize an existing stanza again.
	if err := r.reconcilePgBackRestStanza(context.Background(), cluster, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(calls); !os.IsNotExist(err) {
		t.Fatal("unexpected stanza-create for unchanged configuration")
	}

	// Simulate a repository location change with the same stanza and a transient error.
	if err := os.WriteFile(fail, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := r.reconcilePgBackRestStanza(context.Background(), cluster, true); err == nil {
		t.Fatal("expected initialization failure")
	}
	if r.pgBackRestStanzaCreated.Load() != nil {
		t.Fatal("failed initialization must remain pending")
	}
	if err := os.Remove(fail); err != nil {
		t.Fatal(err)
	}

	// The file is already updated on retry, but initialization must still run.
	if err := r.reconcilePgBackRestStanza(context.Background(), cluster, false); err != nil {
		t.Fatal(err)
	}
	if err := r.reconcilePgBackRestStanza(context.Background(), cluster, false); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(calls) // #nosec G304 -- Test-owned path inside t.TempDir.
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(content), "stanza-create") != 2 {
		t.Fatalf("expected one failed attempt and one successful retry, got %s", content)
	}
}

func TestStanzaInitializationDeferredAfterConfigChange(t *testing.T) {
	for _, reason := range []string{"replica", "suspended"} {
		t.Run(reason, func(t *testing.T) {
			dir := t.TempDir()
			calls := filepath.Join(dir, "calls")
			t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
			t.Setenv("STANZA_CALLS", calls)
			// #nosec G306 -- The fake executable needs owner execute permission inside t.TempDir.
			if err := os.WriteFile(filepath.Join(dir, "pgbackrest"),
				[]byte("#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$STANZA_CALLS\"\n"), 0o700); err != nil {
				t.Fatal(err)
			}
			cluster := &apiv1.Cluster{
				ObjectMeta: metav1.ObjectMeta{Name: "branch"},
				Status:     apiv1.ClusterStatus{CurrentPrimary: "branch-1"},
			}
			if reason == "replica" {
				cluster.Status.CurrentPrimary = "branch-2"
			} else {
				cluster.Annotations = map[string]string{utils.PgBackRestSuspended: "enabled"}
			}
			r := &InstanceReconciler{instance: postgres.NewInstance().WithPodName("branch-1")}
			r.pgBackRestStanzaCreated.Store(ptr.To("branch"))

			// A config change must invalidate initialization without writing to the
			// repository from a replica or a suspended cluster.
			if err := r.reconcilePgBackRestStanza(context.Background(), cluster, true); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(calls); !os.IsNotExist(err) {
				t.Fatal("unexpected stanza-create while initialization is deferred")
			}
			if r.pgBackRestStanzaCreated.Load() != nil {
				t.Fatal("initialization must remain pending")
			}

			// Promotion or resumption must initialize the repository even though
			// the configuration file is already current.
			cluster.Status.CurrentPrimary = "branch-1"
			cluster.Annotations = nil
			if err := r.reconcilePgBackRestStanza(context.Background(), cluster, false); err != nil {
				t.Fatal(err)
			}
			content, err := os.ReadFile(calls) // #nosec G304 -- Test-owned path inside t.TempDir.
			if err != nil {
				t.Fatal(err)
			}
			if string(content) != "--config="+pgbackrest.ConfigFilePath+" --stanza=branch stanza-create --no-online\n" {
				t.Fatalf("unexpected stanza-create invocation: %s", content)
			}
			if created := r.pgBackRestStanzaCreated.Load(); created == nil || *created != "branch" {
				t.Fatal("successful initialization must be recorded")
			}
		})
	}
}
