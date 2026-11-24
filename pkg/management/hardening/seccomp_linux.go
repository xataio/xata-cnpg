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
	"runtime"
	"unsafe"

	seccomp "github.com/elastic/go-seccomp-bpf"
	"golang.org/x/net/bpf"
	"golang.org/x/sys/unix"
)

// deniedSyscalls lists the system calls that the default profile of a
// container runtime allows, or allows on some runtimes, and that PostgreSQL,
// pgBackRest and the instance manager do not need.
//
// PostgreSQL 18 uses io_uring when io_method is io_uring. That setting does
// not work with this list.
var deniedSyscalls = []string{
	// Code without a file on disk.
	"memfd_create", "memfd_secret", "execveat",
	// Access to the memory or the descriptors of another process.
	"ptrace", "process_vm_readv", "process_vm_writev", "process_madvise",
	"pidfd_getfd", "kcmp",
	// Kernel attack surface.
	"io_uring_setup", "io_uring_enter", "io_uring_register",
	"userfaultfd", "perf_event_open", "bpf",
	"add_key", "request_key", "keyctl",
	"unshare", "setns", "mount", "umount2", "pivot_root", "chroot",
	"open_by_handle_at", "name_to_handle_at", "personality",
}

// allowedSocketFamilies lists the socket families that the container needs.
var allowedSocketFamilies = []uint64{
	unix.AF_UNIX, unix.AF_INET, unix.AF_INET6, unix.AF_NETLINK,
}

// socketRule matches a socket call for which all the conditions hold.
func socketRule(conditions ...seccomp.Condition) seccomp.NameWithConditions {
	return seccomp.NameWithConditions{Name: "socket", Conditions: conditions}
}

// seccompPolicy returns a deny-list. A second filter can only restrict
// more: the kernel applies the most restrictive result of all the filters,
// so the profile of the runtime stays in force.
func seccompPolicy(action seccomp.Action) seccomp.Policy {
	// The kernel reads the arguments of socket as 32-bit integers and the
	// filter compares 64 bits. A not-equal test also matches a value with
	// high bits set. An equal test does not, so one rule denies such a value.
	const maxInt32Arg = 1<<32 - 1

	otherFamily := make([]seccomp.Condition, len(allowedSocketFamilies))
	for i, family := range allowedSocketFamilies {
		otherFamily[i] = seccomp.Condition{Argument: 0, Operation: seccomp.NotEqual, Value: family}
	}

	return seccomp.Policy{
		DefaultAction: seccomp.ActionAllow,
		Syscalls: []seccomp.SyscallGroup{
			{Names: deniedSyscalls, Action: action},
			{
				Action: action,
				NamesWithCondtions: []seccomp.NameWithConditions{
					socketRule(otherFamily...),
					socketRule(seccomp.Condition{Argument: 2, Operation: seccomp.GreaterThan, Value: maxInt32Arg}),
					// Landlock does not see Multipath TCP sockets.
					socketRule(seccomp.Condition{Argument: 2, Operation: seccomp.Equal, Value: unix.IPPROTO_MPTCP}),
					// glibc needs NETLINK_ROUTE only.
					socketRule(
						seccomp.Condition{Argument: 0, Operation: seccomp.Equal, Value: unix.AF_NETLINK},
						seccomp.Condition{Argument: 2, Operation: seccomp.NotEqual, Value: unix.NETLINK_ROUTE},
					),
				},
			},
		},
	}
}

// nativeArch returns the AUDIT_ARCH value of the system calls of this
// program.
func nativeArch() (uint32, error) {
	switch runtime.GOARCH {
	case "amd64":
		return unix.AUDIT_ARCH_X86_64, nil
	case "arm64":
		return unix.AUDIT_ARCH_AARCH64, nil
	default:
		return 0, fmt.Errorf("no seccomp filter for the architecture %s", runtime.GOARCH)
	}
}

// archFilter returns a filter that applies action to each system call of
// another ABI, such as a 32-bit call on a 64-bit kernel. The system call
// numbers of seccompPolicy are those of the native ABI, and that filter
// allows every call of another one.
func archFilter(arch uint32, action seccomp.Action) []bpf.Instruction {
	const archOffset = 4 // offsetof(struct seccomp_data, arch)
	if action == seccomp.ActionErrno {
		action |= seccomp.Action(unix.ENOSYS)
	}
	return []bpf.Instruction{
		bpf.LoadAbsolute{Off: archOffset, Size: 4},
		bpf.JumpIf{Cond: bpf.JumpEqual, Val: arch, SkipTrue: 1},
		bpf.RetConstant{Val: uint32(action)},
		bpf.RetConstant{Val: uint32(seccomp.ActionAllow)},
	}
}

// loadFilter installs a filter for the calling thread.
func loadFilter(instructions []bpf.Instruction) error {
	raw, err := bpf.Assemble(instructions)
	if err != nil {
		return err
	}
	filter := make([]unix.SockFilter, len(raw))
	for i, r := range raw {
		filter[i] = unix.SockFilter{Code: r.Op, Jt: r.Jt, Jf: r.Jf, K: r.K}
	}
	//nolint:gosec // bpf.Assemble limits the length
	program := unix.SockFprog{Len: uint16(len(filter)), Filter: &filter[0]}
	//nolint:gosec // the system call takes a pointer to the program
	_, _, errno := unix.Syscall(unix.SYS_SECCOMP, unix.SECCOMP_SET_MODE_FILTER, 0,
		uintptr(unsafe.Pointer(&program)))
	if errno != 0 {
		return fmt.Errorf("seccomp(SECCOMP_SET_MODE_FILTER): %w", errno)
	}
	return nil
}

// applySeccomp installs the two filters. With logOnly the kernel logs a
// denied system call and then allows it.
//
// The filters have no TSYNC flag: harden-exec restricts one thread and then
// calls execve.
func applySeccomp(logOnly bool) error {
	if !seccomp.Supported() {
		return errors.New("the kernel does not support seccomp filters")
	}
	action := seccomp.ActionErrno
	if logOnly {
		action = seccomp.ActionLog
	}

	arch, err := nativeArch()
	if err != nil {
		return err
	}
	if err := loadFilter(archFilter(arch, action)); err != nil {
		return err
	}
	return seccomp.LoadFilter(seccomp.Filter{Policy: seccompPolicy(action)})
}
