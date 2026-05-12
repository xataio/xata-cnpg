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

package pool

import (
	"github.com/jackc/pgx/v5"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Connection profile defaults", func() {
	parseConfig := func() *pgx.ConnConfig {
		cfg, err := pgx.ParseConfig("host=/tmp")
		Expect(err).ToNot(HaveOccurred())
		return cfg
	}

	DescribeTable("pin search_path in the startup packet",
		func(profile ConnectionProfile) {
			cfg := parseConfig()
			profile.Enrich(cfg)

			Expect(cfg.RuntimeParams).To(HaveKeyWithValue("search_path", `"$user", public`))

			// Verify the pre-existing defaults are still present.
			Expect(cfg.RuntimeParams).To(HaveKeyWithValue("client_encoding", "UTF8"))
			Expect(cfg.RuntimeParams).To(HaveKeyWithValue("datestyle", "ISO"))
		},
		Entry("ConnectionProfilePostgresql", ConnectionProfilePostgresql),
		Entry("ConnectionProfilePostgresqlPhysicalReplication", ConnectionProfilePostgresqlPhysicalReplication),
	)

	It("does not pin search_path on the pgbouncer profile", func() {
		// PgBouncer's admin console rejects unknown startup parameters with
		// SQLSTATE 08P01, and the admin connection never reaches PostgreSQL,
		// so there is no CWE-426 vector to defend against here.
		cfg := parseConfig()
		ConnectionProfilePgbouncer.Enrich(cfg)

		Expect(cfg.RuntimeParams).ToNot(HaveKey("search_path"))
		Expect(cfg.RuntimeParams).To(HaveKeyWithValue("client_encoding", "UTF8"))
		Expect(cfg.RuntimeParams).To(HaveKeyWithValue("datestyle", "ISO"))
	})

	It("preserves the synchronous_commit override on the postgresql profile", func() {
		cfg := parseConfig()
		ConnectionProfilePostgresql.Enrich(cfg)

		Expect(cfg.RuntimeParams).To(HaveKeyWithValue("synchronous_commit", "local"))
	})

	It("preserves the replication=1 override on the physical-replication profile", func() {
		cfg := parseConfig()
		ConnectionProfilePostgresqlPhysicalReplication.Enrich(cfg)

		Expect(cfg.RuntimeParams).To(HaveKeyWithValue("replication", "1"))
	})
})
