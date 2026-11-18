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
	"bytes"
	"encoding/json"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/cloudnative-pg/machinery/pkg/log"
)

// logLineOutput is where writeLogLine writes. It is the same *os.File as the
// instance manager logger, so Go serializes the writes of the two.
var logLineOutput io.Writer = os.Stderr

type logField struct {
	key   string
	value any
}

// logLineConfig holds the parts of a log line that do not change after the
// instance manager configures its logger.
type logLineConfig struct {
	enabled bool
	// head is the start of each line, up to the timestamp value.
	head []byte
	// pod is the logging_pod field with its leading comma, or empty.
	pod []byte
}

// logLineSettings computes the configuration at the first line. The log
// pipes start after the instance manager configures its logger.
var logLineSettings = sync.OnceValue(newLogLineConfig)

func newLogLineConfig() logLineConfig {
	levelKey, timeKey := "level", "ts"
	for _, flag := range log.GetFieldsRemapFlags() {
		if v, ok := strings.CutPrefix(flag, "--log-field-level="); ok {
			levelKey = v
		}
		if v, ok := strings.CutPrefix(flag, "--log-field-timestamp="); ok {
			timeKey = v
		}
	}

	var head bytes.Buffer
	head.WriteByte('{')
	writeJSON(&head, levelKey)
	head.WriteByte(':')
	writeJSON(&head, log.InfoLevelString)
	head.WriteByte(',')
	writeJSON(&head, timeKey)
	head.WriteByte(':')

	var pod bytes.Buffer
	if name := os.Getenv("POD_NAME"); name != "" {
		pod.WriteString(`,"logging_pod":`)
		writeJSON(&pod, name)
	}

	return logLineConfig{
		enabled: log.GetLogger().Enabled(),
		head:    head.Bytes(),
		pod:     pod.Bytes(),
	}
}

// writeLogLine writes one info entry in the format of docs/src/logging.md.
// It does not use the instance manager logger, whose sampler drops most
// entries above 100 a second with the same message. It writes to stderr
// even when --log-destination is set.
func writeLogLine(name, msg string, fields ...logField) {
	cfg := logLineSettings()
	if !cfg.enabled {
		return
	}

	var buf bytes.Buffer
	buf.Write(cfg.head)
	buf.WriteByte('"')
	buf.WriteString(time.Now().Format(time.RFC3339Nano))
	buf.WriteString(`","logger":`)
	writeJSON(&buf, name)
	buf.WriteString(`,"msg":`)
	writeJSON(&buf, msg)
	buf.Write(cfg.pod)
	for _, f := range fields {
		value, err := marshalJSON(f.value)
		key := f.key
		if err != nil {
			// zap reports a value it cannot encode in the same way.
			key = f.key + "Error"
			value, _ = marshalJSON(err.Error())
		}
		buf.WriteByte(',')
		writeJSON(&buf, key)
		buf.WriteByte(':')
		buf.Write(value)
	}
	buf.WriteString("}\n")

	_, _ = logLineOutput.Write(buf.Bytes())
}

// writeJSON appends a string as a JSON value.
func writeJSON(buf *bytes.Buffer, s string) {
	b, _ := marshalJSON(s)
	buf.Write(b)
}

// marshalJSON encodes v like zap encodes a reflected value: with
// encoding/json and without HTML escaping.
func marshalJSON(v any) ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(b.Bytes(), []byte("\n")), nil
}
