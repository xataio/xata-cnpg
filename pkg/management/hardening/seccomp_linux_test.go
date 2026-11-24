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
	"encoding/binary"
	"testing"

	seccomp "github.com/elastic/go-seccomp-bpf"
	"golang.org/x/net/bpf"
	"golang.org/x/sys/unix"
)

// seccompData returns a struct seccomp_data for the test VM. The kernel
// reads the 32-bit words of the struct in the byte order of the machine,
// which is little-endian on amd64 and arm64. The VM reads each word as
// big-endian, so the words stay in their little-endian places and only the
// bytes in each word are swapped.
func seccompData(arch uint32, nr uint32, args ...uint64) []byte {
	data := make([]byte, 64)
	binary.BigEndian.PutUint32(data[0:], nr)
	binary.BigEndian.PutUint32(data[4:], arch)
	for i, arg := range args {
		binary.BigEndian.PutUint32(data[16+8*i:], uint32(arg))       //nolint:gosec // the low word
		binary.BigEndian.PutUint32(data[16+8*i+4:], uint32(arg>>32)) //nolint:gosec // the high word
	}
	return data
}

func runFilter(t *testing.T, instructions []bpf.Instruction, data []byte) seccomp.Action {
	t.Helper()
	vm, err := bpf.NewVM(instructions)
	if err != nil {
		t.Fatal(err)
	}
	result, err := vm.Run(data)
	if err != nil {
		t.Fatal(err)
	}
	return seccomp.Action(result) //nolint:gosec // a 32-bit return value
}

func TestSeccompPolicy(t *testing.T) {
	arch, err := nativeArch()
	if err != nil {
		t.Skip(err)
	}
	policy := seccompPolicy(seccomp.ActionErrno)
	instructions, err := policy.Assemble()
	if err != nil {
		t.Fatal(err)
	}

	const (
		allow     = seccomp.ActionAllow
		deny      = seccomp.ActionErrno | seccomp.Action(unix.EPERM)
		highBits  = 1 << 32
		mptcpHigh = highBits | unix.IPPROTO_MPTCP
	)
	tests := []struct {
		name string
		nr   uint32
		args []uint64
		want seccomp.Action
	}{
		{"read", unix.SYS_READ, nil, allow},
		{"execve", unix.SYS_EXECVE, nil, allow},
		{"pidfd_open", unix.SYS_PIDFD_OPEN, nil, allow},
		{"memfd_create", unix.SYS_MEMFD_CREATE, nil, deny},
		{"ptrace", unix.SYS_PTRACE, nil, deny},
		{"process_vm_readv", unix.SYS_PROCESS_VM_READV, nil, deny},
		{"io_uring_setup", unix.SYS_IO_URING_SETUP, nil, deny},
		{"unix socket", unix.SYS_SOCKET, []uint64{unix.AF_UNIX, unix.SOCK_STREAM, 0}, allow},
		{"tcp socket", unix.SYS_SOCKET, []uint64{unix.AF_INET, unix.SOCK_STREAM, unix.IPPROTO_TCP}, allow},
		{"udp6 socket", unix.SYS_SOCKET, []uint64{unix.AF_INET6, unix.SOCK_DGRAM, 0}, allow},
		{"netlink route", unix.SYS_SOCKET, []uint64{unix.AF_NETLINK, unix.SOCK_RAW, unix.NETLINK_ROUTE}, allow},
		{"netlink sock_diag", unix.SYS_SOCKET, []uint64{unix.AF_NETLINK, unix.SOCK_RAW, unix.NETLINK_SOCK_DIAG}, deny},
		{"packet socket", unix.SYS_SOCKET, []uint64{unix.AF_PACKET, unix.SOCK_RAW, 0}, deny},
		{"mptcp", unix.SYS_SOCKET, []uint64{unix.AF_INET, unix.SOCK_STREAM, unix.IPPROTO_MPTCP}, deny},
		{"mptcp with high bits", unix.SYS_SOCKET, []uint64{unix.AF_INET, unix.SOCK_STREAM, mptcpHigh}, deny},
		{"family with high bits", unix.SYS_SOCKET, []uint64{highBits | unix.AF_PACKET, unix.SOCK_RAW, 0}, deny},
		{"netlink with high bits", unix.SYS_SOCKET, []uint64{highBits | unix.AF_NETLINK, unix.SOCK_RAW, 12}, deny},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := runFilter(t, instructions, seccompData(arch, tt.nr, tt.args...))
			if got != tt.want {
				t.Errorf("got %#x, want %#x", uint32(got), uint32(tt.want))
			}
		})
	}
}

func TestArchFilter(t *testing.T) {
	const native, foreign = unix.AUDIT_ARCH_X86_64, unix.AUDIT_ARCH_I386
	instructions := archFilter(native, seccomp.ActionErrno)

	if got := runFilter(t, instructions, seccompData(native, 1)); got != seccomp.ActionAllow {
		t.Errorf("native call: got %#x, want allow", uint32(got))
	}
	want := seccomp.ActionErrno | seccomp.Action(unix.ENOSYS)
	if got := runFilter(t, instructions, seccompData(foreign, 1)); got != want {
		t.Errorf("foreign call: got %#x, want %#x", uint32(got), uint32(want))
	}

	instructions = archFilter(native, seccomp.ActionLog)
	if got := runFilter(t, instructions, seccompData(foreign, 1)); got != seccomp.ActionLog {
		t.Errorf("foreign call in log mode: got %#x, want log", uint32(got))
	}
}
