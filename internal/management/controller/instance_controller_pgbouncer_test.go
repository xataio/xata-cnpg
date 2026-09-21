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
	"database/sql"
	"fmt"

	"github.com/DATA-DOG/go-sqlmock"

	apiv1 "github.com/xataio/xata-cnpg/api/v1"
	"github.com/xataio/xata-cnpg/pkg/management/postgres"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("PgBouncer auth_query integration", func() {
	const (
		roleDetectionQuery       = `SELECT COUNT(*) > 0 FROM pg_catalog.pg_roles WHERE rolname = 'cnpg_pooler_pgbouncer'`
		createUserSearchFunction = `CREATE FUNCTION public.user_search(uname TEXT) ` +
			`RETURNS TABLE (usename name, passwd text) ` +
			`as 'SELECT usename, passwd FROM pg_catalog.pg_shadow WHERE usename=$1;' ` +
			`LANGUAGE sql SECURITY DEFINER ` +
			`SET search_path = pg_catalog, pg_temp`
	)

	var (
		db      *sql.DB
		dbMock  sqlmock.Sqlmock
		r       *InstanceReconciler
		cluster *apiv1.Cluster
	)

	expectFunctionExists := func(exists bool) {
		dbMock.ExpectQuery(fmt.Sprintf(userSearchFunctionDetectionQuery,
			"public", "user_search",
			"SELECT usename, passwd FROM pg_catalog.pg_shadow WHERE usename=$1;",
			"pg_catalog, pg_temp")).
			WillReturnRows(sqlmock.NewRows([]string{""}).AddRow(exists))
	}

	expectPrivilegesGranted := func(granted bool) {
		dbMock.ExpectQuery(fmt.Sprintf(poolerPrivilegesDetectionQuery,
			"cnpg_pooler_pgbouncer", "postgres", "public", "user_search")).
			WillReturnRows(sqlmock.NewRows([]string{""}).AddRow(granted))
	}

	expectPrivilegesRepaired := func() {
		for _, statement := range []string{
			`GRANT CONNECT ON DATABASE postgres TO cnpg_pooler_pgbouncer`,
			`GRANT USAGE ON SCHEMA public TO cnpg_pooler_pgbouncer`,
			`REVOKE ALL ON FUNCTION public.user_search(text) FROM public;`,
			`GRANT EXECUTE ON FUNCTION public.user_search(text) TO cnpg_pooler_pgbouncer`,
		} {
			dbMock.ExpectExec(statement).WillReturnResult(sqlmock.NewResult(0, 0))
		}
	}

	BeforeEach(func() {
		var err error
		db, dbMock, err = sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
		Expect(err).ToNot(HaveOccurred())

		pgInstance := postgres.NewInstance().
			WithNamespace("default").
			WithPodName("cluster-example-1").
			WithClusterName("cluster-example")
		pgInstance.PgData = GinkgoT().TempDir()
		r = &InstanceReconciler{instance: pgInstance}

		cluster = &apiv1.Cluster{
			Status: apiv1.ClusterStatus{
				PoolerIntegrations: &apiv1.PoolerIntegrations{
					PgBouncerIntegration: apiv1.PgBouncerIntegrationStatus{
						Secrets: []string{"pooler-auth-secret"},
					},
				},
			},
		}

		dbMock.ExpectBegin()
		dbMock.ExpectQuery(roleDetectionQuery).
			WillReturnRows(sqlmock.NewRows([]string{""}).AddRow(true))
	})

	AfterEach(func() {
		Expect(dbMock.ExpectationsWereMet()).To(Succeed())
	})

	It("issues no DDL when the function and the privileges are in place", func(ctx SpecContext) {
		expectFunctionExists(true)
		expectPrivilegesGranted(true)
		dbMock.ExpectCommit()

		Expect(r.reconcilePgbouncerAuthUser(ctx, db, cluster)).To(Succeed())
	})

	It("re-applies the privileges when one of them is missing", func(ctx SpecContext) {
		expectFunctionExists(true)
		expectPrivilegesGranted(false)
		expectPrivilegesRepaired()
		dbMock.ExpectCommit()

		Expect(r.reconcilePgbouncerAuthUser(ctx, db, cluster)).To(Succeed())
	})

	It("drops every overload of a function that does not match and recreates it", func(ctx SpecContext) {
		expectFunctionExists(false)
		dbMock.ExpectQuery(fmt.Sprintf(userSearchFunctionOverloadsQuery, "public", "user_search")).
			WillReturnRows(sqlmock.NewRows([]string{""}).AddRow("uname text").AddRow("character varying"))
		dbMock.ExpectExec(`DROP FUNCTION public.user_search(uname text)`).WillReturnResult(sqlmock.NewResult(0, 0))
		dbMock.ExpectExec(`DROP FUNCTION public.user_search(character varying)`).WillReturnResult(sqlmock.NewResult(0, 0))
		dbMock.ExpectExec(createUserSearchFunction).WillReturnResult(sqlmock.NewResult(0, 0))
		expectPrivilegesGranted(false)
		expectPrivilegesRepaired()
		dbMock.ExpectCommit()

		Expect(r.reconcilePgbouncerAuthUser(ctx, db, cluster)).To(Succeed())
	})
})
