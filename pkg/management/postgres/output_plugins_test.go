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
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	postgresClient "github.com/cloudnative-pg/cnpg-i/pkg/postgres"

	apiv1 "github.com/xataio/xata-cnpg/api/v1"
	"github.com/xataio/xata-cnpg/pkg/management/postgres/constants"
)

const (
	describeWithGUC    = "wal_level\tpostmaster\noutput_plugin_libraries\tsuperuser\n"
	describeWithoutGUC = "wal_level\tpostmaster\n"
	defaultPluginsLine = "output_plugin_libraries = 'pgoutput, test_decoding, wal2json'\n"
)

func TestParseOutputPluginSupport(t *testing.T) {
	tests := map[string]struct {
		out     string
		runErr  error
		want    bool
		wantErr bool
	}{
		"has the parameter":         {out: describeWithGUC, want: true},
		"lacks the parameter":       {out: describeWithoutGUC},
		"name is only in a text":    {out: describeWithoutGUC + "other\tsee output_plugin_libraries\n"},
		"name is only a prefix":     {out: describeWithoutGUC + "output_plugin_libraries_x\tuser\n"},
		"reaped, has the parameter": {out: describeWithGUC, runErr: syscall.ECHILD, want: true},
		"reaped, row is cut":        {out: "output_plugin_libraries\tsuper", runErr: syscall.ECHILD, wantErr: true},
		"reaped, lacks it":          {out: describeWithoutGUC, runErr: syscall.ECHILD, wantErr: true},
		"reaped without output":     {runErr: syscall.ECHILD, wantErr: true},
		"command failed":            {out: describeWithGUC, runErr: errors.New("exit status 1"), wantErr: true},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			got, err := parseOutputPluginSupport([]byte(tc.out), tc.runErr)
			if (err != nil) != tc.wantErr {
				t.Fatalf("got error %v, want error %v", err, tc.wantErr)
			}
			if got != tc.want {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

// fakePostgres makes the instance run a script in place of postgres.
func fakePostgres(t *testing.T, script string) string {
	t.Helper()
	name := filepath.Join(t.TempDir(), "postgres")
	if err := os.WriteFile(name, []byte("#!/bin/sh\n"+script+"\n"), 0o700); err != nil { //nolint:gosec
		t.Fatal(err)
	}
	t.Setenv("POSTGRES_NAME", name)
	return name
}

func refreshConfig(t *testing.T, instance *Instance, params map[string]string) (string, string) {
	t.Helper()
	cluster := &apiv1.Cluster{}
	cluster.Name = "test"
	cluster.Spec.PostgresConfiguration.Parameters = params
	if _, err := instance.RefreshConfigurationFilesFromCluster(
		t.Context(), cluster, false, postgresClient.OperationType_TYPE_RECONCILE,
	); err != nil {
		t.Fatal(err)
	}
	conf, err := os.ReadFile(filepath.Join(instance.PgData, constants.PostgresqlCustomConfigurationFile))
	if err != nil {
		t.Fatal(err)
	}
	return string(conf), instance.ConfigSha256
}

func newTestInstance(t *testing.T) *Instance {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "PG_VERSION"), []byte("18\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return &Instance{PgData: dir}
}

func TestRefreshConfigurationOutputPlugins(t *testing.T) {
	const custom = "output_plugin_libraries = 'pgoutput'\n"
	tests := map[string]struct {
		script      string
		params      map[string]string
		wantDefault bool
		wantCustom  bool
	}{
		"supported":   {script: "printf '" + describeWithGUC + "'", wantDefault: true},
		"unsupported": {script: "printf '" + describeWithoutGUC + "'"},
		"probe fails": {script: "exit 1"},
		"cluster value comes last": {
			script:      "printf '" + describeWithGUC + "'",
			params:      map[string]string{"output_plugin_libraries": "pgoutput"},
			wantDefault: true,
			wantCustom:  true,
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			fakePostgres(t, tc.script)
			conf, _ := refreshConfig(t, newTestInstance(t), tc.params)
			if got := strings.HasPrefix(conf, defaultPluginsLine); got != tc.wantDefault {
				t.Fatalf("got default %v, want %v in:\n%s", got, tc.wantDefault, conf)
			}
			if !tc.wantCustom {
				return
			}
			// PostgreSQL uses the last value in the file.
			if strings.LastIndex(conf, custom) <= strings.Index(conf, defaultPluginsLine) {
				t.Fatalf("the cluster value is not last in:\n%s", conf)
			}
		})
	}
}

// Instances on different minors must report the same hash, or the operator
// stops the rolling update.
func TestOutputPluginsDoNotChangeConfigHash(t *testing.T) {
	fakePostgres(t, "printf '"+describeWithGUC+"'")
	newConf, newSHA := refreshConfig(t, newTestInstance(t), nil)
	fakePostgres(t, "printf '"+describeWithoutGUC+"'")
	oldConf, oldSHA := refreshConfig(t, newTestInstance(t), nil)

	if newConf == oldConf {
		t.Fatal("got the same configuration for both minors")
	}
	if newSHA != oldSHA {
		t.Fatalf("got hashes %s and %s, want them equal", newSHA, oldSHA)
	}
}

func TestOutputPluginProbeCache(t *testing.T) {
	name := fakePostgres(t, "exit 1")
	instance := newTestInstance(t)
	if conf, _ := refreshConfig(t, instance, nil); strings.Contains(conf, defaultPluginsLine) {
		t.Fatal("got the default after a failed probe")
	}

	script := "#!/bin/sh\nprintf '" + describeWithGUC + "'\n"
	if err := os.WriteFile(name, []byte(script), 0o700); err != nil { //nolint:gosec
		t.Fatal(err)
	}
	if conf, _ := refreshConfig(t, instance, nil); !strings.Contains(conf, defaultPluginsLine) {
		t.Fatal("a failed probe was cached")
	}

	if err := os.Remove(name); err != nil {
		t.Fatal(err)
	}
	if conf, _ := refreshConfig(t, instance, nil); !strings.Contains(conf, defaultPluginsLine) {
		t.Fatal("a successful probe was not cached")
	}
}

// pg_rewind copies custom.conf from an instance that can run another minor.
func TestRestoreDefaultOutputPlugins(t *testing.T) {
	const (
		rest   = "max_connections = '100'\n"
		custom = "output_plugin_libraries = 'pgoutput'\n"
	)
	tests := map[string]struct {
		script string
		conf   string
		want   string
	}{
		"older binary drops the default": {
			script: "printf '" + describeWithoutGUC + "'", conf: defaultPluginsLine + rest, want: rest,
		},
		"failed probe drops the default": {script: "exit 1", conf: defaultPluginsLine + rest, want: rest},
		"newer binary adds the default": {
			script: "printf '" + describeWithGUC + "'", conf: rest, want: defaultPluginsLine + rest,
		},
		"newer binary keeps the file": {
			script: "printf '" + describeWithGUC + "'", conf: defaultPluginsLine + rest, want: defaultPluginsLine + rest,
		},
		"cluster value stays": {
			script: "printf '" + describeWithoutGUC + "'", conf: defaultPluginsLine + rest + custom, want: rest + custom,
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			fakePostgres(t, tc.script)
			instance := newTestInstance(t)
			file := filepath.Join(instance.PgData, constants.PostgresqlCustomConfigurationFile)
			if err := os.WriteFile(file, []byte(tc.conf), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := instance.RestoreDefaultOutputPlugins(t.Context()); err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(file) //nolint:gosec
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tc.want {
				t.Fatalf("got:\n%s\nwant:\n%s", got, tc.want)
			}
		})
	}
}

// liveCheck runs the check against a server that gives one answer.
func liveCheck(t *testing.T, instance *Instance, now time.Time, setting, source, file string) bool {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close() //nolint:errcheck
	mock.ExpectQuery("pg_settings").WillReturnRows(
		sqlmock.NewRows([]string{"setting", "source", "sourcefile"}).AddRow(setting, source, file))
	return instance.outputPluginsNeedReload(t.Context(), db, now)
}

func ownedInstance() *Instance {
	return &Instance{outputPluginsCheck: outputPluginsCheck{owned: true}}
}

const (
	builtInPlugins  = "pgoutput, test_decoding"
	customConfPath  = "/var/lib/postgresql/data/pgdata/custom.conf"
	autoConfPath    = "/var/lib/postgresql/data/pgdata/postgresql.auto.conf"
	fileSource      = "configuration file"
	builtInSource   = "default"
	commandLineName = "command line"
)

// The live server decides if a reload is needed, not the file or a flag.
func TestOutputPluginsNeedReload(t *testing.T) {
	tests := map[string]struct {
		setting, source, file string
		want                  bool
	}{
		"line not loaded":             {setting: builtInPlugins, source: builtInSource, want: true},
		"line loaded":                 {setting: defaultOutputPluginLibraries, source: fileSource, file: customConfPath},
		"removed value still live":    {setting: "pgoutput", source: fileSource, file: customConfPath, want: true},
		"value from ALTER SYSTEM":     {setting: "pgoutput", source: fileSource, file: autoConfPath},
		"value from the command line": {setting: "pgoutput", source: commandLineName},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			instance := ownedInstance()
			if got := liveCheck(t, instance, time.Now(), tc.setting, tc.source, tc.file); got != tc.want {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			// The check ends only when the server agrees.
			if instance.outputPluginsCheck.agreed == tc.want {
				t.Fatalf("got agreed %v for reload %v", !tc.want, tc.want)
			}
		})
	}
}

// A failed query must not stop the reconciliation, and the check stays due.
func TestOutputPluginsQueryError(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close() //nolint:errcheck
	mock.ExpectQuery("pg_settings").WillReturnError(errors.New("connection refused"))
	instance := ownedInstance()
	if instance.outputPluginsNeedReload(t.Context(), db, time.Now()) || instance.outputPluginsCheck.agreed {
		t.Fatal("a failed query asked for a reload or ended the check")
	}
}

// An error before the signal, for example from the quorum reset, must not
// start the wait. The next reconciliation asks again.
func TestOutputPluginRequestsWithoutSignal(t *testing.T) {
	instance := ownedInstance()
	now := time.Now()
	for i := range 5 {
		if !liveCheck(t, instance, now, builtInPlugins, builtInSource, "") {
			t.Fatalf("request %d: got no reload, and no signal was sent", i)
		}
	}
}

// PostgreSQL can accept the signal and reject the file. Then the setting
// and pg_conf_load_time() do not change. The sent signal must start the wait.
func TestOutputPluginReloadsAreThrottled(t *testing.T) {
	instance := ownedInstance()
	start := time.Now()
	if !liveCheck(t, instance, start, builtInPlugins, builtInSource, "") {
		t.Fatal("got no reload for a stale server")
	}
	instance.OutputPluginsReloadSent()

	for _, wait := range []time.Duration{0, time.Minute, outputPluginsRetryInterval - time.Minute} {
		if liveCheck(t, instance, start.Add(wait), builtInPlugins, builtInSource, "") {
			t.Fatalf("got a reload %s after a sent signal", wait)
		}
	}
	if instance.outputPluginsCheck.agreed {
		t.Fatal("the check ended for a stale server")
	}
	// time.Now() in OutputPluginsReloadSent is later than start
	later := start.Add(outputPluginsRetryInterval + time.Minute)
	if !liveCheck(t, instance, later, builtInPlugins, builtInSource, "") {
		t.Fatal("got no reload after the retry interval")
	}

	// A server that agrees ends the wait.
	instance.OutputPluginsReloadSent()
	if liveCheck(t, instance, later, defaultOutputPluginLibraries, fileSource, customConfPath) {
		t.Fatal("got a reload for a server that loaded the line")
	}
	if !instance.outputPluginsCheck.agreed || !instance.outputPluginsCheck.retryAfter.IsZero() {
		t.Fatal("the check did not end")
	}
}

// The default is in force only if the rendered configuration has no value.
// The value can come from the Cluster or from a CNPG-I plugin.
func TestOutputPluginsOwnership(t *testing.T) {
	const rest = "max_connections = '100'\n"
	has := "printf '" + describeWithGUC + "'"
	tests := map[string]struct {
		script string
		conf   string
		want   bool
	}{
		"supported, no value":   {script: has, conf: rest, want: true},
		"supported, with value": {script: has, conf: rest + "output_plugin_libraries = 'pgoutput'\n"},
		"other parameter name":  {script: has, conf: rest + "output_plugin_libraries_x = 'a'\n", want: true},
		"uppercase name":        {script: has, conf: rest + "OUTPUT_PLUGIN_LIBRARIES = 'pgoutput'\n"},
		"mixed case, no spaces": {script: has, conf: rest + "Output_Plugin_Libraries='pgoutput'\n"},
		"name only in a value":  {script: has, conf: rest + "a = 'output_plugin_libraries = b'\n", want: true},
		"binary lacks the GUC":  {script: "printf '" + describeWithoutGUC + "'", conf: rest},
		"probe fails":           {script: "exit 1", conf: rest},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			fakePostgres(t, tc.script)
			instance := &Instance{outputPluginsCheck: outputPluginsCheck{agreed: true}}
			instance.addDefaultOutputPlugins(t.Context(), tc.conf)
			if got := instance.outputPluginsCheck.owned; got != tc.want {
				t.Fatalf("got owned %v, want %v", got, tc.want)
			}
			// The check is due again after the value goes away.
			if !tc.want && instance.outputPluginsCheck.agreed {
				t.Fatal("the answer of the server was kept while the default was not in force")
			}
			if instance.OutputPluginsNeedReload(t.Context()) && !tc.want {
				t.Fatal("got a reload while the default was not in force")
			}
		})
	}
}
