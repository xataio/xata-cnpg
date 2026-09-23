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
	"database/sql"
	"fmt"
	"path"

	"github.com/cloudnative-pg/machinery/pkg/fileutils"
	"github.com/cloudnative-pg/machinery/pkg/log"
	"github.com/jackc/pgx/v5"

	"github.com/xataio/xata-cnpg/pkg/configfile"
	"github.com/xataio/xata-cnpg/pkg/management/postgres/constants"
)

const (
	// AdoptedMarkerFile is written into PGDATA once it has been adapted to run
	// as an independent primary, so that a pod restart does not run the
	// procedure again against an already promoted data directory.
	AdoptedMarkerFile = ".xata-adopted"

	// standbySignalFile marks a data directory as a standby for PostgreSQL 12
	// and beyond.
	standbySignalFile = "standby.signal"
)

// foreignReplicationOptions are the recovery settings `pg_basebackup -R` writes
// into postgresql.auto.conf. They point the data directory at the primary it
// was copied from, which is not ours to follow.
var foreignReplicationOptions = []string{
	"primary_conninfo",
	"primary_slot_name",
	"recovery_target_timeline",
	"restore_command",
}

// AdoptForeignStandby adapts a PGDATA that arrived from a physical replica
// managed outside this cluster, so that it can be started as an independent
// primary.
//
// It applies only to a noop bootstrap, where PGDATA is mounted from an external
// source after the pod has started, and only when the data directory looks like
// a standby that CloudNativePG has never managed: standby.signal present and no
// override.conf. A replica this operator created always has override.conf, so
// its replication settings are left alone.
//
// The standby.signal file is deliberately kept. With primary_conninfo gone the
// instance replays the WAL it already has, opens as a hot standby, and the
// instance reconciler promotes it, which ends recovery with an end-of-recovery
// record rather than a blocking checkpoint and forks a new timeline.
func AdoptForeignStandby(ctx context.Context, pgData string) error {
	contextLogger := log.FromContext(ctx).WithName("adopt_foreign_standby")

	adopt, err := shouldAdopt(pgData)
	if err != nil {
		return err
	}
	if !adopt {
		return nil
	}

	contextLogger.Info("Adapting a data directory taken from a foreign standby",
		"pgData", pgData)

	// Drop the files that must not survive into a new instance: the stale
	// postmaster state, the replication slots inherited from the source, and
	// the caches rebuilt on startup. This also removes standby.signal.
	if err := fileutils.RemoveRestoreExcludedFiles(ctx, pgData); err != nil {
		return fmt.Errorf("while cleaning up the adopted PGDATA: %w", err)
	}

	// Put standby.signal back, so the instance replays its WAL in standby mode
	// and is promoted rather than crash recovered.
	if err := createStandbySignal(pgData); err != nil {
		return fmt.Errorf("while restoring the standby signal: %w", err)
	}

	if err := removeForeignReplicationOptions(pgData); err != nil {
		return err
	}

	// A foreign postgresql.conf has never been told to read what the operator
	// manages. Only the initdb and restore paths wire that up, so without this
	// every setting in custom.conf is silently ignored — down to
	// unix_socket_directories, which leaves PostgreSQL unable to start. The
	// override.conf include is appended later, by the configuration refresh,
	// which keeps it last so its recovery settings still win.
	if _, err := configfile.EnsureIncludes(path.Join(pgData, "postgresql.conf"),
		constants.PostgresqlCustomConfigurationFile,
	); err != nil {
		return fmt.Errorf("while including the operator configuration: %w", err)
	}

	if err := fileutils.CreateEmptyFile(path.Join(pgData, AdoptedMarkerFile)); err != nil {
		return fmt.Errorf("while writing the adoption marker: %w", err)
	}

	contextLogger.Info("Data directory adapted, the instance will be promoted once it has replayed its WAL")

	return nil
}

// shouldAdopt reports whether the data directory is a standby from outside this
// cluster that has not been adapted yet.
func shouldAdopt(pgData string) (bool, error) {
	adopted, err := fileutils.FileExists(path.Join(pgData, AdoptedMarkerFile))
	if err != nil {
		return false, fmt.Errorf("while looking for the adoption marker: %w", err)
	}
	if adopted {
		return false, nil
	}

	standby, err := fileutils.FileExists(path.Join(pgData, standbySignalFile))
	if err != nil {
		return false, fmt.Errorf("while looking for the standby signal: %w", err)
	}
	if !standby {
		return false, nil
	}

	// A standby this operator created always carries override.conf, written
	// before PostgreSQL is started. Its presence means the data directory is
	// ours and its replication settings must be preserved.
	managed, err := fileutils.FileExists(
		path.Join(pgData, constants.PostgresqlOverrideConfigurationFile))
	if err != nil {
		return false, fmt.Errorf("while looking for the override configuration: %w", err)
	}

	return !managed, nil
}

// removeForeignReplicationOptions strips the recovery settings that point at
// the source cluster's primary out of postgresql.auto.conf.
func removeForeignReplicationOptions(pgData string) error {
	autoConfPath := path.Join(pgData, "postgresql.auto.conf")

	lines, err := fileutils.ReadFileLines(autoConfPath)
	if err != nil {
		return fmt.Errorf("while reading postgresql.auto.conf: %w", err)
	}

	_, err = fileutils.WriteLinesToFile(autoConfPath,
		configfile.RemoveOptionsFromConfigurationContents(lines, foreignReplicationOptions...))
	if err != nil {
		return fmt.Errorf("while rewriting postgresql.auto.conf: %w", err)
	}

	return nil
}

// platformSuperuserRole is the non-login role the platform grants its managed
// roles into. A cluster bootstrapped by this platform gets it from initdb's
// post-init SQL, and a clone inherits it; a data directory adopted from a
// foreign primary has never had it.
const platformSuperuserRole = "xata_superuser"

// platformApplicationRole is the login role the operator's managed roles create
// and keep in sync, and platformApplicationDatabase is the database the console,
// the SQL gateway and the tooling assume every branch has.
const (
	platformApplicationRole     = "xata"
	platformApplicationDatabase = "xata"
)

// platformSuperuserGrants are the predefined roles platformSuperuserRole holds.
// Kept in step with the post-init SQL the platform runs at initdb time.
var platformSuperuserGrants = []string{
	"pg_read_all_data",
	"pg_write_all_data",
	"pg_maintain",
	"pg_monitor",
	"pg_stat_scan_tables",
	"pg_signal_backend",
	"pg_checkpoint",
	"pg_read_all_settings",
	"pg_read_all_stats",
	"pg_create_subscription",
	"pg_use_reserved_connections",
}

// EnsureAdoptedPlatformObjects creates what an adopted data directory is missing
// for the rest of the platform to work against it: the role the managed roles
// are granted into, and the application database everything assumes exists.
//
// It runs only against a data directory this node adopted from a foreign
// standby, and only once it is a writable primary. Each step checks for what it
// creates, so the whole thing is idempotent and converges over reconciles —
// which matters, because the application role is created asynchronously by the
// operator's managed roles and may not exist the first time through.
//
// TODO: seeding this belongs with whatever provisions a source cluster for
// adoption, rather than being repaired here after the fact.
func EnsureAdoptedPlatformObjects(ctx context.Context, pgData string, db *sql.DB) error {
	adopted, err := fileutils.FileExists(path.Join(pgData, AdoptedMarkerFile))
	if err != nil {
		return fmt.Errorf("while looking for the adoption marker: %w", err)
	}
	if !adopted {
		return nil
	}

	if err := ensurePlatformSuperuserRole(ctx, db); err != nil {
		return err
	}

	if err := ensureApplicationDatabase(ctx, db); err != nil {
		return err
	}

	return ensureApplicationRoleDatabaseAccess(ctx, db)
}

// ensurePlatformSuperuserRole creates the non-login role the managed application
// role asks to be a member of. A foreign cluster has never had it, and the
// membership grant fails while it is absent. No password is involved: the
// application role itself is created and kept in sync by the operator's managed
// roles, from the secret the control plane published.
func ensurePlatformSuperuserRole(ctx context.Context, db *sql.DB) error {
	contextLogger := log.FromContext(ctx).WithName("adopt_foreign_standby")

	var exists bool
	if err := db.QueryRowContext(ctx,
		"SELECT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = $1)",
		platformSuperuserRole,
	).Scan(&exists); err != nil {
		return fmt.Errorf("while looking for role %q: %w", platformSuperuserRole, err)
	}
	if exists {
		return nil
	}

	contextLogger.Info("Creating the platform role missing from an adopted data directory",
		"role", platformSuperuserRole)

	// The role name and the grants are compile-time constants, so the quoting
	// below cannot carry anything from outside this package.
	if _, err := db.ExecContext(ctx,
		fmt.Sprintf("CREATE ROLE %s NOLOGIN", pgx.Identifier{platformSuperuserRole}.Sanitize()),
	); err != nil {
		return fmt.Errorf("while creating role %q: %w", platformSuperuserRole, err)
	}

	for _, grant := range platformSuperuserGrants {
		if _, err := db.ExecContext(ctx, fmt.Sprintf("GRANT %s TO %s",
			pgx.Identifier{grant}.Sanitize(),
			pgx.Identifier{platformSuperuserRole}.Sanitize(),
		)); err != nil {
			return fmt.Errorf("while granting %q to %q: %w", grant, platformSuperuserRole, err)
		}
	}

	return nil
}

// ensureApplicationDatabase creates the application database, empty, when the
// adopted cluster does not have one. A cluster this platform created gets it
// from initdb; an adopted one carries whatever the source happened to name its
// database, and the console, the SQL gateway and the tooling all assume the
// platform's name is present.
//
// The database is created first and handed to the application role afterwards,
// separately, because that role is created asynchronously by the managed roles
// and is usually absent on the first pass.
//
// TODO: an empty database is a placeholder. The adopted data lives under the
// source's own database name, so either adoption should carry that name
// forward or the source has to follow this platform's convention.
func ensureApplicationDatabase(ctx context.Context, db *sql.DB) error {
	contextLogger := log.FromContext(ctx).WithName("adopt_foreign_standby")

	var exists bool
	if err := db.QueryRowContext(ctx,
		"SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname = $1)",
		platformApplicationDatabase,
	).Scan(&exists); err != nil {
		return fmt.Errorf("while looking for database %q: %w", platformApplicationDatabase, err)
	}

	if !exists {
		contextLogger.Info("Creating the application database missing from an adopted data directory",
			"database", platformApplicationDatabase)

		// No encoding or locale is given, so the database inherits template1 and
		// therefore whatever the source cluster was initialised with. Asking for
		// anything else here would require template0 and could fail outright.
		if _, err := db.ExecContext(ctx,
			fmt.Sprintf("CREATE DATABASE %s", pgx.Identifier{platformApplicationDatabase}.Sanitize()),
		); err != nil {
			return fmt.Errorf("while creating database %q: %w", platformApplicationDatabase, err)
		}
	}

	// Hand it over once the managed application role shows up.
	var owner string
	if err := db.QueryRowContext(ctx,
		`SELECT pg_get_userbyid(datdba) FROM pg_database WHERE datname = $1`,
		platformApplicationDatabase,
	).Scan(&owner); err != nil {
		return fmt.Errorf("while reading the owner of %q: %w", platformApplicationDatabase, err)
	}
	if owner == platformApplicationRole {
		return nil
	}

	var roleExists bool
	if err := db.QueryRowContext(ctx,
		"SELECT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = $1)",
		platformApplicationRole,
	).Scan(&roleExists); err != nil {
		return fmt.Errorf("while looking for role %q: %w", platformApplicationRole, err)
	}
	if !roleExists {
		// The managed roles have not created it yet; a later reconcile will.
		return nil
	}

	contextLogger.Info("Handing the application database to the application role",
		"database", platformApplicationDatabase, "owner", platformApplicationRole)

	if _, err := db.ExecContext(ctx, fmt.Sprintf("ALTER DATABASE %s OWNER TO %s",
		pgx.Identifier{platformApplicationDatabase}.Sanitize(),
		pgx.Identifier{platformApplicationRole}.Sanitize(),
	)); err != nil {
		return fmt.Errorf("while setting the owner of %q: %w", platformApplicationDatabase, err)
	}

	return nil
}

// ensureApplicationRoleDatabaseAccess grants the application role every
// database-level privilege on every database of the adopted cluster. The
// databases that came with the source are owned by whatever roles the source
// had, and the application role inherits nothing from them: without this it
// cannot even create a schema in the database the adopted data actually lives
// in. Table and schema access is not handled here, it comes with the
// pg_read_all_data and pg_write_all_data memberships of platformSuperuserRole.
//
// Template databases are left alone. Anything granted on them would be copied
// into every database created afterwards, and the platform never connects to
// them. Databases already fully granted are skipped, so a reconcile against a
// converged cluster does nothing.
func ensureApplicationRoleDatabaseAccess(ctx context.Context, db *sql.DB) error {
	contextLogger := log.FromContext(ctx).WithName("adopt_foreign_standby")

	var roleExists bool
	if err := db.QueryRowContext(ctx,
		"SELECT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = $1)",
		platformApplicationRole,
	).Scan(&roleExists); err != nil {
		return fmt.Errorf("while looking for role %q: %w", platformApplicationRole, err)
	}
	if !roleExists {
		// The managed roles have not created it yet; a later reconcile will.
		return nil
	}

	// ALL PRIVILEGES on a database is exactly CREATE, CONNECT and TEMP, so a
	// database where the role already holds the three needs nothing. The
	// privileges are tested one at a time: given a comma-separated list,
	// has_database_privilege reports whether any of them is held, and CONNECT
	// and TEMP are granted to PUBLIC by default, which would make every
	// database look done.
	rows, err := db.QueryContext(ctx, `
		SELECT datname
		FROM pg_database
		WHERE NOT datistemplate
		  AND NOT (has_database_privilege($1, oid, 'CREATE')
		       AND has_database_privilege($1, oid, 'CONNECT')
		       AND has_database_privilege($1, oid, 'TEMP'))
		ORDER BY datname`,
		platformApplicationRole,
	)
	if err != nil {
		return fmt.Errorf("while listing the databases %q lacks access to: %w",
			platformApplicationRole, err)
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			contextLogger.Error(closeErr, "while closing the database list")
		}
	}()

	var databases []string
	for rows.Next() {
		var datname string
		if err := rows.Scan(&datname); err != nil {
			return fmt.Errorf("while reading the database list: %w", err)
		}
		databases = append(databases, datname)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("while reading the database list: %w", err)
	}

	for _, datname := range databases {
		contextLogger.Info("Granting the application role full access to an adopted database",
			"database", datname, "role", platformApplicationRole)

		if _, err := db.ExecContext(ctx, fmt.Sprintf("GRANT ALL PRIVILEGES ON DATABASE %s TO %s",
			pgx.Identifier{datname}.Sanitize(),
			pgx.Identifier{platformApplicationRole}.Sanitize(),
		)); err != nil {
			return fmt.Errorf("while granting %q access to database %q: %w",
				platformApplicationRole, datname, err)
		}
	}

	return nil
}
