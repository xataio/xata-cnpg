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

package run

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/cloudnative-pg/machinery/pkg/log"
)

const (
	pgdataWaitInterval = 30 * time.Millisecond
	xataReadyMarker    = ".xata-ready"
)

// waitForPGData waits for the PGDATA directory or a ready marker file to
// appear. In standard PVC setups, PGDATA already exists and this returns
// immediately. For NVMe-oF fast-wake scenarios, the storage may be mounted
// after the pod starts; this function polls until PGDATA or the marker file
// appears, waiting indefinitely until the context is cancelled.
func waitForPGData(ctx context.Context, pgData string) error {
	// Fast path: PGDATA already exists
	if _, err := os.Stat(pgData); err == nil {
		return nil
	}

	contextLogger := log.FromContext(ctx)
	contextLogger.Info("PGDATA not yet available, waiting for mount to appear",
		"pgData", pgData)

	volumeRoot := filepath.Dir(pgData)
	markerPath := filepath.Join(volumeRoot, xataReadyMarker)

	ticker := time.NewTicker(pgdataWaitInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("context cancelled while waiting for PGDATA: %w", ctx.Err())

		case <-ticker.C:
			if _, err := os.Stat(pgData); err == nil {
				contextLogger.Info("PGDATA directory is now available", "pgData", pgData)
				return nil
			}
			if _, err := os.Stat(markerPath); err == nil {
				contextLogger.Info("Ready marker file detected", "marker", markerPath)
				return nil
			}
		}
	}
}
