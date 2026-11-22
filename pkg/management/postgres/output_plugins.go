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
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/cloudnative-pg/machinery/pkg/log"

	"github.com/xataio/xata-cnpg/pkg/management/postgres/constants"
)

const (
	outputPluginLibrariesParameter = "output_plugin_libraries"
	defaultOutputPluginLibraries   = "pgoutput, test_decoding, wal2json"
	defaultOutputPluginsLine       = outputPluginLibrariesParameter + " = '" + defaultOutputPluginLibraries + "'\n"
	describeConfigTimeout          = 10 * time.Second
	outputPluginsRetryInterval     = 10 * time.Minute
)

// addDefaultOutputPlugins puts the default output plugin allowlist first in
// conf, so that a value from the Cluster or from a plugin wins. The line is
// not part of the configuration hash, which must be equal on instances with
// different minors.
func (instance *Instance) addDefaultOutputPlugins(ctx context.Context, conf string) string {
	supported := instance.detectOutputPluginSupport(ctx)
	// The default is in force only if the rendered configuration has no value
	instance.outputPluginsCheck.owned = supported && !setsOutputPlugins(conf)
	if !instance.outputPluginsCheck.owned {
		// Ask the server again when the default is in force
		instance.outputPluginsCheck.agreed = false
		instance.outputPluginsCheck.retryAfter = time.Time{}
	}
	if !supported {
		return conf
	}
	return defaultOutputPluginsLine + conf
}

func (instance *Instance) detectOutputPluginSupport(ctx context.Context) bool {
	if instance.supportsOutputPlugins == nil {
		supported, err := instance.probeOutputPluginSupport(ctx)
		if err != nil {
			// Omit the line. PostgreSQL does not start with an unknown parameter.
			log.FromContext(ctx).Warning("Cannot detect support for output_plugin_libraries, will retry",
				"err", err.Error())
			return false
		}
		instance.supportsOutputPlugins = &supported
	}
	return *instance.supportsOutputPlugins
}

// setsOutputPlugins reports if a rendered configuration sets the parameter.
func setsOutputPlugins(conf string) bool {
	for line := range strings.Lines(conf) {
		// PostgreSQL ignores the case of a parameter name
		key, _, found := strings.Cut(line, "=")
		if found && strings.EqualFold(strings.TrimSpace(key), outputPluginLibrariesParameter) {
			return true
		}
	}
	return false
}

// RestoreDefaultOutputPlugins makes the default line in an existing
// custom.conf match the local binary. pg_rewind copies the file from an
// instance that can run another minor. The other lines stay as they are.
func (instance *Instance) RestoreDefaultOutputPlugins(ctx context.Context) error {
	name := filepath.Join(instance.PgData, constants.PostgresqlCustomConfigurationFile)
	conf, err := os.ReadFile(name) // #nosec G304
	if err != nil {
		return err
	}
	restored := instance.addDefaultOutputPlugins(ctx, strings.TrimPrefix(string(conf), defaultOutputPluginsLine))
	_, err = InstallPgDataFileContent(ctx, instance.PgData, restored, constants.PostgresqlCustomConfigurationFile)
	return err
}

// outputPluginsCheck is the state of the check of the running server.
type outputPluginsCheck struct {
	// owned is set when the default line is the value that must be in force
	owned bool
	// agreed is set when the server reported the default
	agreed bool
	// retryAfter is the end of the wait after a corrective reload
	retryAfter time.Time
}

// OutputPluginsNeedReload reports if the running server does not use the
// default line that is in custom.conf. That occurs when a reconciliation or
// an instance manager ends between the write of the file and the reload.
// It asks the server until the server agrees, and never fails the caller.
// The caller must call OutputPluginsReloadSent after it sent the reload.
func (instance *Instance) OutputPluginsNeedReload(ctx context.Context) bool {
	check := &instance.outputPluginsCheck
	if !check.owned || check.agreed {
		return false
	}
	db, err := instance.GetSuperUserDB()
	if err != nil {
		return false
	}
	return instance.outputPluginsNeedReload(ctx, db, time.Now())
}

// OutputPluginsReloadSent starts the wait before the next corrective reload.
// A server that cannot load the file must not get a reload on each reconciliation.
func (instance *Instance) OutputPluginsReloadSent() {
	instance.outputPluginsCheck.retryAfter = time.Now().Add(outputPluginsRetryInterval)
}

func (instance *Instance) outputPluginsNeedReload(ctx context.Context, db *sql.DB, now time.Time) bool {
	contextLogger := log.FromContext(ctx)
	check := &instance.outputPluginsCheck

	var setting, source, sourceFile string
	err := db.QueryRowContext(ctx,
		"SELECT setting, source, COALESCE(sourcefile, '') FROM pg_catalog.pg_settings WHERE name = $1",
		outputPluginLibrariesParameter).Scan(&setting, &source, &sourceFile)
	if err != nil {
		contextLogger.Warning("Cannot read output_plugin_libraries from the server, will retry", "err", err.Error())
		return false
	}

	// The server is stale if it did not read the line, or if it still has a
	// value that the configuration no longer sets. A value from another file
	// has priority.
	fromCustomConf := filepath.Base(sourceFile) == constants.PostgresqlCustomConfigurationFile
	if source != "default" && (!fromCustomConf || setting == defaultOutputPluginLibraries) {
		check.agreed = true
		check.retryAfter = time.Time{}
		return false
	}
	if now.Before(check.retryAfter) {
		return false
	}
	contextLogger.Info("Live output plugin setting is stale", "setting", setting, "source", source)
	return true
}

// probeOutputPluginSupport asks the postgres executable, because the server is
// not running when the configuration is first written.
func (instance *Instance) probeOutputPluginSupport(ctx context.Context) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, describeConfigTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, GetPostgresExecutableName(), "--describe-config") // #nosec
	cmd.Env = instance.buildPostgresEnv()
	out, err := cmd.Output()
	return parseOutputPluginSupport(out, err)
}

func parseOutputPluginSupport(out []byte, runErr error) (bool, error) {
	// PostgresOrphansReaper can collect the exit status first. A complete row
	// still proves support. Its absence proves nothing, so that case is an error.
	if runErr != nil && !errors.Is(runErr, syscall.ECHILD) {
		return false, fmt.Errorf("describing the postgres configuration: %w", runErr)
	}
	row := []byte(outputPluginLibrariesParameter + "\t")
	for line := range bytes.Lines(out) {
		if bytes.HasPrefix(line, row) && bytes.HasSuffix(line, []byte("\n")) {
			return true, nil
		}
	}
	if runErr != nil {
		return false, fmt.Errorf("describing the postgres configuration: %w", runErr)
	}
	return false, nil
}
