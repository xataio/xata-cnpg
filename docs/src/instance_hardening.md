---
id: instance_hardening
sidebar_position: 105
title: Instance hardening
---

# Instance hardening
<!-- SPDX-License-Identifier: CC-BY-4.0 -->

CloudNativePG can apply Linux kernel restrictions to the PostgreSQL process tree
in an instance Pod. The [instance manager](instance_manager.md) applies the
restrictions before starting PostgreSQL, and child processes inherit them. The
`no-dumpable` feature protects the instance manager itself.

Hardening is off by default. The settings apply during normal instance Pod
startup. Bootstrap, join, and restore Jobs and sidecars use their usual
security settings.

## Enable hardening for a cluster

Create a ConfigMap in the same namespace as the Cluster. Set
`CNPG_INSTANCE_HARDENING` to a comma-separated list of features and reference the
ConfigMap from `spec.envFrom`. The Cluster's `spec.env` field reserves names
that start with `CNPG_`.

This example protects the instance manager and audits system calls made by the
PostgreSQL process tree.

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: instance-hardening
data:
  CNPG_INSTANCE_HARDENING: "no-dumpable,seccomp-log"
---
apiVersion: postgresql.cnpg.io/v1
kind: Cluster
metadata:
  name: cluster-example
spec:
  instances: 3

  envFrom:
    - configMapRef:
        name: instance-hardening

  storage:
    size: 1Gi
```

## Features

| Feature | Effect | Requirements and limits |
| --- | --- | --- |
| `no-dumpable` | Restricts access to the instance manager's process state by other processes running as the same user. | The PostgreSQL launcher applies the cluster's core dump filter. Changes to the filter take effect when PostgreSQL restarts. |
| `mdwe` | Restricts writable executable memory and prevents mappings from gaining execute permission. | Linux 6.3 or later. PostgreSQL's LLVM just-in-time compilation is disabled. |
| `landlock` | Restricts reads and writes to the paths needed by the instance and leaves the default Kubernetes service account token mount out of the allowlist. | Landlock ABI 2 (Linux 5.19). Check any custom extension, backup, or credential mounts. |
| `landlock-exec` | Allows program execution from the image's system paths and the instance manager's binary directory. | Landlock ABI 1 (Linux 5.13). Covers program execution only. |
| `landlock-net` | Limits TCP binds to the PostgreSQL port and optionally restricts outbound TCP by destination port. | Landlock ABI 4 (Linux 6.7). The rules apply to ports on ordinary TCP sockets. Use `seccomp` to restrict Multipath TCP. |
| `landlock-scope` | Limits signals and abstract UNIX socket connections initiated by PostgreSQL to its own Landlock scope. | Landlock ABI 6 (Linux 6.12). The instance manager can still signal PostgreSQL. |
| `seccomp` | Restricts selected system calls and socket families in addition to the container runtime policy. | Linux with seccomp filtering on `amd64` or `arm64`. PostgreSQL 18 or later needs an I/O method other than `io_uring`. |
| `seccomp-log` | Logs matching system calls to the node's kernel audit log while the container runtime policy continues to apply. | Requires seccomp logging support. Select either `seccomp-log` or `seccomp`. |

With `landlock-net`, set `CNPG_INSTANCE_HARDENING_TCP_CONNECT` in the same
ConfigMap to allow outbound TCP only to the listed destination ports. Include
ports used by replication, local services, and backup endpoints. Leaving the
setting empty applies only the bind restriction. Use a Kubernetes network
policy for rules by address.

## Roll out or disable hardening

Check the required kernel features on every node that can run an instance Pod.
For Landlock, confirm that the node kernel enables Landlock and exposes the ABI
required by the features you select. An unknown feature or a failure to apply a
restriction prevents PostgreSQL from starting. The instance manager logs the
startup error.

Review audit logs with `seccomp-log` before switching to `seccomp`. On a test
cluster, check that replicas start, connections and extensions work, and backups,
WAL archiving, failover, and recovery still succeed. With Landlock enabled, also
check that container exec policies permit the manager binary at
`/controller/bin/manager`.

Changing the ConfigMap reference in `spec.envFrom` starts a
[rolling update](rolling_update.md). If you edit the ConfigMap in place, run
`kubectl cnpg restart cluster-example` to apply the new environment. To disable
hardening, remove the settings and roll the Pods again. See
[environment variables](cluster_conf.md#environment-variables) for details.
