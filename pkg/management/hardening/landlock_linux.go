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

package hardening

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/landlock-lsm/go-landlock/landlock"
	llsys "github.com/landlock-lsm/go-landlock/landlock/syscall"

	"github.com/xataio/xata-cnpg/pkg/postgres"
)

const (
	accessRead = landlock.AccessFSSet(llsys.AccessFSReadFile | llsys.AccessFSReadDir)

	// accessWriteFile lets a process change files that exist.
	accessWriteFile = landlock.AccessFSSet(llsys.AccessFSWriteFile | llsys.AccessFSTruncate)

	// accessWrite lets a process create, change, rename and remove files.
	accessWrite = accessWriteFile | landlock.AccessFSSet(
		llsys.AccessFSRemoveDir|llsys.AccessFSRemoveFile|llsys.AccessFSMakeDir|
			llsys.AccessFSMakeReg|llsys.AccessFSMakeSock|llsys.AccessFSMakeFifo|
			llsys.AccessFSMakeSym|llsys.AccessFSRefer)

	accessExecute = landlock.AccessFSSet(llsys.AccessFSExecute)

	// handledFS is every filesystem right up to Landlock ABI 5, without
	// execute. No rule grants the creation of device nodes or an ioctl on a
	// device, so the process tree has neither.
	handledFS = accessRead | accessWrite | landlock.AccessFSSet(
		llsys.AccessFSMakeChar|llsys.AccessFSMakeBlock|llsys.AccessFSIoctlDev)

	scratch = postgres.ScratchDataDirectory
)

// The ServiceAccount token is below /run, and /run is the scratch volume.
// A Landlock rule covers everything below its directory, across mount
// points. No rule can therefore name /run, /tmp or /controller itself: each
// rule names one subdirectory of the scratch volume.
var (
	// readOnlyDirs holds what the process tree reads and never writes.
	//
	// Keep procfs read-only to prevent writes to process memory. This
	// also prevents PostgreSQL from adjusting its own OOM score.
	readOnlyDirs = []string{
		"/usr", "/bin", "/sbin", "/lib", "/lib64", "/opt", "/etc", "/sys", "/proc",
		"/extensions", "/projected", "/plugins",
		scratch + "/certificates",
		scratch + "/pgbackrest",
		scratch + "/external",
		managerDir,
	}

	// writableDirs holds the directories that the process tree owns.
	writableDirs = []string{
		"/var/lib/postgresql",
		"/dev/shm",
		postgres.SocketDirectory,
		postgres.TemporaryDirectory,
		postgres.SpoolDirectory,
		scratch + "/wal-restore-spool",
	}

	// writableFileDirs holds directories in which the process tree writes
	// to files that exist: device nodes and the log pipes of the instance
	// manager.
	writableFileDirs = []string{"/dev", postgres.LogPath}

	// createdDirs must exist before PostgreSQL starts. The instance manager
	// creates some of them later, and a rule cannot name a missing path.
	createdDirs = []string{
		scratch + "/certificates",
		scratch + "/pgbackrest",
		scratch + "/external",
		postgres.SpoolDirectory,
		scratch + "/wal-restore-spool",
		postgres.TemporaryDirectory,
		postgres.SocketDirectory,
		postgres.LogPath,
	}

	// tokenFileEnvVars identifies workload identity token files used
	// by backup clients.
	tokenFileEnvVars = []string{"AWS_WEB_IDENTITY_TOKEN_FILE", "AZURE_FEDERATED_TOKEN_FILE"}
)

// serviceAccountDir is the default Kubernetes ServiceAccount token mount,
// expressed under /run instead of its /var/run alias.
const serviceAccountDir = "/run/secrets/kubernetes.io/serviceaccount"

// coversServiceAccount reports whether a rule on dir gives access to the
// ServiceAccount token.
func coversServiceAccount(dir string) bool {
	dir = filepath.Clean(dir)
	if rest, ok := strings.CutPrefix(dir, "/var/run"); ok && (rest == "" || rest[0] == '/') {
		dir = "/run" + rest
	}
	if dir == "/" {
		return true
	}
	within := func(path, parent string) bool {
		return path == parent || strings.HasPrefix(path, parent+"/")
	}
	return within(serviceAccountDir, dir) || within(dir, serviceAccountDir)
}

// tokenDirs returns the directories of the workload identity tokens.
func tokenDirs() ([]string, error) {
	var dirs []string
	for _, name := range tokenFileEnvVars {
		file := os.Getenv(name)
		if file == "" {
			continue
		}
		dir := filepath.Dir(file)
		if resolved, err := filepath.EvalSymlinks(dir); err == nil {
			dir = resolved
		}
		if coversServiceAccount(dir) {
			return nil, fmt.Errorf("%s is in %s, which also holds the ServiceAccount token", name, dir)
		}
		dirs = append(dirs, dir)
	}
	return dirs, nil
}

func requireLandlockABI(minimum int) error {
	abi, err := llsys.LandlockGetABIVersion()
	if err != nil {
		return fmt.Errorf("landlock is not available: %w", err)
	}
	if abi < minimum {
		return fmt.Errorf("the kernel has Landlock ABI %d, the feature needs %d", abi, minimum)
	}
	return nil
}

// requireManagerDir checks that [ProtectManager] moved the instance manager
// binary. A rule cannot name a missing directory.
func requireManagerDir() error {
	if _, err := os.Stat(managerDir); err != nil {
		return errors.Join(errors.New("the instance manager binary is not in its own directory"), err)
	}
	return nil
}

// applyLandlock limits filesystem access for the process tree. The policy
// excludes the default Kubernetes ServiceAccount mount and grants read-only
// access to the instance manager binary directory.
func applyLandlock() error {
	// ABI 2 adds the right to rename across directories. Without it the
	// kernel denies every such rename.
	if err := requireLandlockABI(2); err != nil {
		return err
	}
	if err := requireManagerDir(); err != nil {
		return err
	}
	for _, dir := range createdDirs {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
	}
	tokens, err := tokenDirs()
	if err != nil {
		return err
	}

	// BestEffort removes the rights that the kernel does not know. The ABI
	// check above keeps it from removing all of them.
	return landlock.Config{HandledAccessFS: handledFS}.BestEffort().RestrictPaths(
		landlock.PathAccess(accessRead, slices.Concat(readOnlyDirs, tokens)...).IgnoreIfMissing(),
		landlock.PathAccess(accessRead|accessWrite, writableDirs...).IgnoreIfMissing(),
		landlock.PathAccess(accessRead|accessWriteFile, writableFileDirs...),
	)
}

// applyLandlockExec limits program execution to the image and the instance
// manager. Executable memory mappings are outside this policy's scope.
func applyLandlockExec() error {
	if err := requireLandlockABI(1); err != nil {
		return err
	}
	if err := requireManagerDir(); err != nil {
		return err
	}

	return landlock.Config{HandledAccessFS: accessExecute}.RestrictPaths(
		landlock.PathAccess(accessExecute, "/usr", "/bin", "/sbin", "/lib", "/lib64", "/opt").IgnoreIfMissing(),
		landlock.PathAccess(accessExecute, managerDir),
	)
}

// tcpConnectPorts reads the port list of [TCPConnectEnvVar].
func tcpConnectPorts(list string) ([]uint16, error) {
	var ports []uint16
	for field := range strings.SplitSeq(list, ",") {
		field = strings.TrimSpace(field)
		if field == "" {
			continue
		}
		port, err := strconv.ParseUint(field, 10, 16)
		if err != nil {
			return nil, fmt.Errorf("invalid port in %s: %w", TCPConnectEnvVar, err)
		}
		ports = append(ports, uint16(port))
	}
	return ports, nil
}

// applyLandlockNet limits the TCP ports. The process tree can bind only the
// PostgreSQL port. It does not cover UDP, and seccomp must deny Multipath
// TCP sockets, which Landlock does not see.
func applyLandlockNet() error {
	if err := requireLandlockABI(4); err != nil {
		return err
	}
	ports, err := tcpConnectPorts(os.Getenv(TCPConnectEnvVar))
	if err != nil {
		return err
	}

	handled := landlock.AccessNetSet(llsys.AccessNetBindTCP)
	rules := []landlock.Rule{landlock.BindTCP(postgres.ServerPort)}
	if len(ports) > 0 {
		handled |= landlock.AccessNetSet(llsys.AccessNetConnectTCP)
		for _, port := range ports {
			rules = append(rules, landlock.ConnectTCP(port))
		}
	}

	return landlock.Config{HandledAccessNet: handled}.RestrictNet(rules...)
}

// applyLandlockScope stops signals and abstract UNIX socket connections
// from the process tree to processes outside it, such as the instance
// manager.
func applyLandlockScope() error {
	if err := requireLandlockABI(6); err != nil {
		return err
	}
	return landlock.V6.RestrictScoped()
}
