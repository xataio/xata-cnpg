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
	"fmt"
	"os"
	"os/exec"
	"sync"
	"syscall"

	"github.com/cloudnative-pg/machinery/pkg/log"
)

// TLSServerPort is the default port for the pgbackrest TLS server.
const TLSServerPort = 8432

// TLSServer manages the pgbackrest TLS server subprocess. The server
// accepts connections from other pgbackrest instances for backup-standby
// operations, replacing SSH as the transport mechanism.
type TLSServer struct {
	mu      sync.Mutex
	process *os.Process
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
	process, err := checkForExistingTLSServer(TLSServerPIDFile, pgbackrestBinary)
	if err != nil {
		return err
	}
	if process != nil {
		// An online instance manager upgrade uses syscall.Exec, which leaves the
		// pgbackrest child running while replacing all manager state. Adopt that
		// process just as the new manager adopts an existing PostgreSQL postmaster.
		s.process = process
		s.running = true
		contextLog.Info("adopted running pgbackrest TLS server", "pid", process.Pid)
		go s.monitor(contextLog, process, func() error {
			state, err := process.Wait()
			if err != nil {
				return err
			}
			if !state.Success() {
				return &exec.ExitError{ProcessState: state}
			}
			return nil
		})
		return nil
	}

	args := []string{"--config=" + ConfigFilePath, "server"}

	cmd := exec.Command(pgbackrestBinary, args...) // #nosec G204
	if err := cmd.Start(); err != nil {
		return err
	}
	if err := writeTLSServerPIDFile(cmd.Process.Pid); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return fmt.Errorf("writing pgbackrest TLS server PID file: %w", err)
	}

	s.process = cmd.Process
	s.running = true

	contextLog.Info("pgbackrest TLS server started", "pid", cmd.Process.Pid)

	go s.monitor(contextLog, cmd.Process, cmd.Wait)

	return nil
}

func (s *TLSServer) monitor(contextLog log.Logger, process *os.Process, wait func() error) {
	err := wait()

	s.mu.Lock()
	if s.process == process {
		s.process = nil
		s.running = false
		removeTLSServerPIDFile(process.Pid)
	}
	s.mu.Unlock()

	if err != nil {
		contextLog.Info("pgbackrest TLS server exited", "err", err)
	} else {
		contextLog.Info("pgbackrest TLS server exited cleanly")
	}
}

// Stop sends SIGTERM to the pgbackrest server for graceful shutdown.
func (s *TLSServer) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.running || s.process == nil {
		return
	}

	_ = s.process.Signal(syscall.SIGTERM)
}

// Reload sends SIGHUP to the pgbackrest server, causing it to re-read
// its configuration and TLS certificates from disk.
func (s *TLSServer) Reload() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.running || s.process == nil {
		return
	}

	_ = s.process.Signal(syscall.SIGHUP)
}

// IsRunning returns true if the TLS server process is alive.
func (s *TLSServer) IsRunning() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running
}
