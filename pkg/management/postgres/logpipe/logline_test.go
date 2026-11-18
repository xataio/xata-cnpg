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

package logpipe

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cloudnative-pg/machinery/pkg/log"
	"github.com/spf13/cobra"
)

// configureInstanceManagerLogger configures the machinery logger as the
// instance manager does, writing to a file in the test directory. It returns
// the path of that file and restores the previous state when the test ends.
func configureInstanceManagerLogger(t *testing.T, args ...string) string {
	t.Helper()
	dest := filepath.Join(t.TempDir(), "instance-manager.log")
	prev := log.GetLogger().GetLogger()

	var lf log.Flags
	flags := (&cobra.Command{}).Flags()
	lf.AddFlags(flags)
	if err := flags.Parse(append([]string{"--log-destination=" + dest}, args...)); err != nil {
		t.Fatal(err)
	}
	lf.ConfigureLogging()
	logLineSettings = sync.OnceValue(newLogLineConfig)

	t.Cleanup(func() {
		reset := []string{"--log-level=info", "--log-destination=", "--log-field-level=", "--log-field-timestamp="}
		if err := flags.Parse(reset); err != nil {
			t.Error(err)
		}
		log.SetLogger(prev)
		logLineSettings = sync.OnceValue(newLogLineConfig)
	})
	return dest
}

// captureLogLines sends the output of writeLogLine to a buffer until the
// test ends.
func captureLogLines(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := logLineOutput
	logLineOutput = &buf
	t.Cleanup(func() { logLineOutput = prev })
	return &buf
}

func readJSONLines(t *testing.T, data []byte) []map[string]any {
	t.Helper()
	var out []map[string]any
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(nil, 1024*1024)
	for sc.Scan() {
		var m map[string]any
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			t.Fatalf("invalid JSON line %q: %v", sc.Text(), err)
		}
		out = append(out, m)
	}
	return out
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path) //nolint:gosec
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// compareLines checks that each line has a valid timestamp under timeKey,
// then compares the lines without it.
func compareLines(t *testing.T, timeKey string, want, got []map[string]any) {
	t.Helper()
	if len(want) == 0 || len(want) != len(got) {
		t.Fatalf("got %d lines, want %d (and more than 0)", len(got), len(want))
	}
	for i := range want {
		for _, m := range []map[string]any{want[i], got[i]} {
			ts, _ := m[timeKey].(string)
			if _, err := time.Parse(time.RFC3339Nano, ts); err != nil {
				t.Errorf("bad %s value in %v: %v", timeKey, m, err)
			}
			delete(m, timeKey)
		}
		if !reflect.DeepEqual(want[i], got[i]) {
			t.Errorf("line %d differs from the instance manager logger\n got: %v\nwant: %v", i, got[i], want[i])
		}
	}
}

func TestLogRecordWriterMatchesInstanceManagerFormat(t *testing.T) {
	t.Setenv("POD_NAME", "cluster-1")
	dest := configureInstanceManagerLogger(t, "--log-field-level=severity", "--log-field-timestamp=time")
	buf := captureLogLines(t)

	f, err := os.Open("testdata/pgaudit.csv")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	spy := SpyRecordWriter{}
	p := LogPipe{record: NewPgAuditLoggingDecorator(), fieldsValidator: LogFieldValidator}
	if err := p.streamLogFromCSVFile(t.Context(), f, &spy); err != nil {
		t.Fatal(err)
	}
	if len(spy.records) == 0 {
		t.Fatal("no records in testdata/pgaudit.csv")
	}

	var w LogRecordWriter
	for _, r := range spy.records {
		log.WithName(r.GetName()).Info(logRecordKey, logRecordKey, r)
		w.Write(r)
	}

	compareLines(t, "time", readJSONLines(t, readFile(t, dest)), readJSONLines(t, buf.Bytes()))
}

func TestRawLineLogPipeMatchesInstanceManagerFormat(t *testing.T) {
	t.Setenv("POD_NAME", "cluster-1")
	dest := configureInstanceManagerLogger(t)
	buf := captureLogLines(t)

	const source = "/controller/log/postgres"
	lines := []string{
		`2026-11-18 00:12:16.000 UTC [26] LOG:  redirecting log output to logging collector process`,
		`a "quoted" <tag> & é line`,
	}
	p := NewRawLineLogPipe(source, LoggingCollectorRecordName)
	for _, line := range lines {
		log.WithName(LoggingCollectorRecordName).WithValues("source", source).Info(line)
		p.handler([]byte(line))
	}

	compareLines(t, "ts", readJSONLines(t, readFile(t, dest)), readJSONLines(t, buf.Bytes()))
}

func TestWriteLogLineFollowsLogLevel(t *testing.T) {
	configureInstanceManagerLogger(t, "--log-level=warning")
	buf := captureLogLines(t)

	writeLogLine("postgres", logRecordKey, logField{logRecordKey, "x"})
	if buf.Len() != 0 {
		t.Errorf("wrote %q at --log-level=warning, want nothing", buf.String())
	}
}

func TestWriteLogLineDoesNotSample(t *testing.T) {
	const n = 1000
	dest := configureInstanceManagerLogger(t)
	buf := captureLogLines(t)

	for range n {
		log.WithName("postgres").Info(logRecordKey, logRecordKey, "x")
		writeLogLine("postgres", logRecordKey, logField{logRecordKey, "x"})
	}

	if ref := strings.Count(string(readFile(t, dest)), "\n"); ref >= n {
		t.Fatalf("instance manager logger wrote %d of %d lines, want it to sample", ref, n)
	}
	if got := strings.Count(buf.String(), "\n"); got != n {
		t.Errorf("writeLogLine wrote %d of %d lines", got, n)
	}
}
