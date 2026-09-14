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
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/xataio/xata-cnpg/pkg/executablehash"
	"github.com/xataio/xata-cnpg/pkg/management/postgres"
	pgstatus "github.com/xataio/xata-cnpg/pkg/postgres"
	"github.com/xataio/xata-cnpg/pkg/versions"

	. "github.com/onsi/gomega"
)

func TestWaitingSlotStatus(t *testing.T) {
	for name, upgrading := range map[string]bool{"idle": false, "upgrading": true} {
		t.Run(name, func(t *testing.T) {
			g := NewWithT(t)
			// No database or PGDATA exists. The endpoint must report manager
			// metadata without connecting to PostgreSQL or electing a primary.
			instance := &postgres.Instance{}
			instance.SetWaitingForPGData(true)
			instance.InstanceManagerIsUpgrading.Store(upgrading)
			ws := &remoteWebserverEndpoints{instance: instance}
			response := httptest.NewRecorder()
			ws.pgStatus(response, httptest.NewRequest(http.MethodGet, "/pg/status", nil))

			g.Expect(response.Code).To(Equal(http.StatusOK))
			g.Expect(response.Header().Get("Content-Type")).To(Equal("application/json"))
			var status pgstatus.PostgresqlStatus
			g.Expect(json.Unmarshal(response.Body.Bytes(), &status)).To(Succeed())
			hash, err := executablehash.Get()
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(status.ExecutableHash).To(Equal(hash))
			g.Expect(status.ExecutableHash).NotTo(BeEmpty())
			g.Expect(status.InstanceArch).To(Equal(instance.GetArchitecture()))
			g.Expect(status.InstanceManagerVersion).To(Equal(versions.Version))
			g.Expect(status.IsInstanceManagerUpgrading).To(Equal(upgrading))
			g.Expect(status.MightBeUnavailable).To(BeTrue())
			g.Expect(status.IsPrimary).To(BeFalse())
			g.Expect(status.SystemID).To(BeEmpty())
		})
	}
}
