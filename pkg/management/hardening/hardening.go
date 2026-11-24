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

// Package hardening applies kernel restrictions to the processes of an
// instance pod. The restrictions are opt-in, one feature at a time.
package hardening

import (
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"
)

// EnvVar is the environment variable that holds a comma-separated list of
// the features to enable.
const EnvVar = "CNPG_INSTANCE_HARDENING"

// TCPConnectEnvVar is the environment variable that holds the
// comma-separated TCP ports that the PostgreSQL process tree can connect
// to. When it is empty, [LandlockNet] restricts only bind.
const TCPConnectEnvVar = "CNPG_INSTANCE_HARDENING_TCP_CONNECT"

// A Feature is one restriction.
type Feature string

// The features, in the order in which they are applied.
const (
	// NoDumpable clears the instance manager's dumpable flag to restrict
	// ptrace and procfs access by other processes with the same UID.
	NoDumpable Feature = "no-dumpable"

	// MDWE denies writable-and-executable memory in the PostgreSQL process
	// tree. It needs Linux 6.3. It disables the LLVM JIT.
	MDWE Feature = "mdwe"

	// Landlock limits filesystem access for the PostgreSQL process tree,
	// including access to the default Kubernetes ServiceAccount mount.
	// It needs Linux 5.19.
	Landlock Feature = "landlock"

	// LandlockExec limits execve to the image and the instance manager.
	LandlockExec Feature = "landlock-exec"

	// LandlockNet limits the TCP ports of the PostgreSQL process tree. It
	// needs Linux 6.7.
	LandlockNet Feature = "landlock-net"

	// SeccompLog runs [Seccomp] in audit mode. The kernel logs matching
	// system calls and allows them.
	SeccompLog Feature = "seccomp-log"

	// Seccomp adds a system call deny-list on top of the runtime's profile.
	Seccomp Feature = "seccomp"
)

// features lists every feature in the order in which it is applied.
var features = [...]Feature{
	NoDumpable,
	MDWE,
	Landlock,
	LandlockExec,
	LandlockNet,
	SeccompLog,
	Seccomp,
}

// subtree reports whether harden-exec applies the feature to the PostgreSQL
// process tree. The other features restrict the instance manager itself.
func (f Feature) subtree() bool {
	return f != NoDumpable
}

// Features is a set of restrictions, in the order in which they are applied.
type Features []Feature

// Parse reads a comma-separated list of features. It ignores empty fields
// and duplicates.
func Parse(s string) (Features, error) {
	var set Features
	for name := range strings.SplitSeq(s, ",") {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if !slices.Contains(features[:], Feature(name)) {
			return nil, fmt.Errorf("unknown hardening feature %q", name)
		}
		if !set.Has(Feature(name)) {
			set = append(set, Feature(name))
		}
	}
	if set.Has(Seccomp) && set.Has(SeccompLog) {
		return nil, fmt.Errorf("hardening features %q and %q exclude each other", Seccomp, SeccompLog)
	}

	var result Features
	for _, f := range features {
		if set.Has(f) {
			result = append(result, f)
		}
	}
	return result, nil
}

// FromEnv reads the features from [EnvVar].
func FromEnv() (Features, error) {
	return Parse(os.Getenv(EnvVar))
}

// Has reports whether the set contains f.
func (fs Features) Has(f Feature) bool {
	return slices.Contains(fs, f)
}

// String returns the comma-separated list that [Parse] reads.
func (fs Features) String() string {
	names := make([]string, len(fs))
	for i, f := range fs {
		names[i] = string(f)
	}
	return strings.Join(names, ",")
}

// subtree returns the features that harden-exec applies.
func (fs Features) subtree() Features {
	var result Features
	for _, f := range fs {
		if f.subtree() {
			result = append(result, f)
		}
	}
	return result
}

// usesLandlock reports whether a Landlock feature is enabled. The manager
// uses this check to prepare the binary directory before starting PostgreSQL.
func (fs Features) usesLandlock() bool {
	return slices.ContainsFunc(fs, func(f Feature) bool {
		return strings.HasPrefix(string(f), string(Landlock))
	})
}

// PostgresOptions returns the postgres command-line options that the
// features need.
//
// With [MDWE] the LLVM JIT cannot make its code executable, and jit is on by
// default, so a query above jit_above_cost would fail. An unknown
// jit_provider makes the JIT unavailable, and no session can change it.
func (fs Features) PostgresOptions() []string {
	if fs.Has(MDWE) {
		return []string{"-c", "jit_provider=disabled_by_mdwe"}
	}
	return nil
}

// selfExe is the running instance manager. The link stays valid after an
// online upgrade replaces the file on disk.
const selfExe = "/proc/self/exe"

// WrapCommand makes cmd start through the "instance harden-exec"
// subcommand, which applies the restrictions and then executes the original
// program. The restrictions pass to every descendant and no descendant can
// remove them. WrapCommand does nothing when no feature applies to cmd.
//
// Go cannot run code between fork and exec, so the restrictions need a
// second exec of the instance manager.
//
// With [NoDumpable] the instance manager cannot write its own
// /proc/self/coredump_filter, from which its children inherit the filter.
// harden-exec then writes coredumpFilter for the program instead.
func WrapCommand(cmd *exec.Cmd, fs Features, coredumpFilter string) {
	if !fs.Has(NoDumpable) {
		coredumpFilter = ""
	}
	sub := fs.subtree()
	if len(sub) == 0 && coredumpFilter == "" {
		return
	}
	args := []string{selfExe, "instance", "harden-exec", "--features", sub.String()}
	if coredumpFilter != "" {
		args = append(args, "--coredump-filter", coredumpFilter)
	}
	// The program path, then its complete argument list with argv[0].
	args = append(args, "--", cmd.Path)
	args = append(args, cmd.Args...)
	cmd.Path = selfExe
	cmd.Args = args
}
