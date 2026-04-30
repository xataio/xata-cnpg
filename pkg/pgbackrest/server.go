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

package pgbackrest

import (
	"context"
	"os/exec"
	"sync"

	"github.com/cloudnative-pg/machinery/pkg/log"
)

// TLSServerPort is the default port for the pgbackrest TLS server.
const TLSServerPort = 8432

// TLSServer manages the pgbackrest TLS server subprocess. The server
// accepts connections from other pgbackrest instances for backup-standby
// operations, replacing SSH as the transport mechanism.
type TLSServer struct {
	mu      sync.Mutex
	cmd     *exec.Cmd
	running bool
}

// Start launches the pgbackrest server as a background subprocess.
// The server reads its TLS configuration from the pgbackrest config file.
// This is a no-op if the server is already running.
func (s *TLSServer) Start(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.running {
		return nil
	}

	contextLog := log.FromContext(ctx)

	args := []string{"--config=" + ConfigFilePath, "server"}

	cmd := exec.CommandContext(ctx, pgbackrestBinary, args...) // #nosec G204
	if err := cmd.Start(); err != nil {
		return err
	}

	s.cmd = cmd
	s.running = true

	contextLog.Info("pgbackrest TLS server started", "pid", cmd.Process.Pid)

	// Monitor the process in a goroutine — if it exits, mark as not running
	// so the reconciler can restart it.
	go func() {
		err := cmd.Wait()
		s.mu.Lock()
		s.running = false
		s.mu.Unlock()

		if err != nil {
			contextLog.Info("pgbackrest TLS server exited", "err", err)
		} else {
			contextLog.Info("pgbackrest TLS server exited cleanly")
		}
	}()

	return nil
}

// Stop sends SIGTERM to the pgbackrest server and waits for it to exit.
func (s *TLSServer) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.running || s.cmd == nil || s.cmd.Process == nil {
		return
	}

	_ = s.cmd.Process.Kill()
	s.running = false
}

// IsRunning returns true if the TLS server process is alive.
func (s *TLSServer) IsRunning() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running
}
