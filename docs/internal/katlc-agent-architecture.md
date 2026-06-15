# Katlc Agent Architecture

Status: working design.

This document scopes `katlc` as the long-running node agent for KatlOS hosts.
It defines how `katlctl` talks to a node, how accepted work is recorded and
executed, and where command/query separation helps without turning Katl into a
Talos-style machine controller.

## Summary

`katlc` runs on every KatlOS host as a systemd-managed service. `katlctl` runs
on the operator workstation and connects directly to each node's `katlc` TCP
gRPC management endpoint.

```text
katlctl on workstation
  -> TCP gRPC
    -> katlc-agent.service on one KatlOS node
      -> validate request
      -> write journal-first OperationRecord
      -> execute accepted operation through the agent executor
      -> expose redacted status and artifacts through query APIs
```

`katlctl` may orchestrate multiple nodes because each `katlc` instance is
node-local. `katlc` does not know the whole cluster rollout plan beyond the
explicit request it accepted for its node.

## Boundary

`katlc` owns node-local KatlOS state:

```text
GenerationSpec and GenerationStatus records
boot selection records
OperationRecords and operation material
generated sysext/confext payload selection
node-local health and readiness evidence
redacted diagnostics and status projections
```

`katlctl` owns operator workflow:

```text
loading inventory or compiled plans
selecting the bootstrap init node
sequencing requests across nodes
polling or watching returned operation IDs
writing disposable client summaries
writing explicit client-side output such as kubeconfig files
```

`katlctl` must not SSH to nodes, run remote shell commands, write
`/var/lib/katl`, create generation records, create operation records, or become
the recovery database.

## Agent Service

The target service shape is:

```text
katlc-agent.service
  ExecStart=/usr/bin/katlc agent serve --listen tcp://<management-address>
  RequiresMountsFor=/var/lib/katl
  After=local-fs.target katl-generation-activate.service
```

Systemd supervises the agent process, normal KatlOS services, boot health
targets, rollback targets, and service dependencies. Systemd is not the
operation RPC, dispatcher, or executor API. The target runtime must not use
templated operation units to re-execute `katlc` for normal operation execution.

Optional local `katlc` commands may exist for break-glass diagnostics, but they
must be wrappers around the same state model or agent API. They must not create
a second supported operation submission path.

## Command And Query Split

Katl should use a command/query split internally because it matches the
operation model:

```text
commands mutate node state
queries read redacted node status
events record what happened
projections make status cheap and operator-friendly
```

This is useful CQRS vocabulary, but it is an implementation pattern, not a user
contract and not a requirement to adopt a framework.

### Commands

Commands are accepted only through the agent API or internal agent startup paths.
They validate intent, acquire locks, write operation records, and then execute
bounded work.

Day-one command shapes:

```text
SubmitOperation
ValidateConfig
ApplyGeneration
StageGeneration
RollbackGeneration, when implemented
```

Later command shapes:

```text
RetryOperation
RepairOperation
KubeadmUpgrade
KubeadmReset
RestoreEtcdSnapshot
```

Every mutating command must either:

```text
refuse before mutation
record a dry-run result without creating authoritative state
accept and create an OperationRecord before mutation
```

### Queries

Queries never mutate lifecycle state.

Day-one query shapes:

```text
GetNodeStatus
GetOperation
WatchOperation
ListGenerations
GetGeneration
```

Query responses are read models built from authoritative state and live probes.
They are allowed to be stale or redacted as long as they identify the source
operation, generation, journal sequence, and any refusal reason needed for
operator action.

## Event Journal And Projections

The authoritative operation source is the journal under:

```text
/var/lib/katl/operations/<operation-id>/journal/
```

`record.json` is a rebuildable projection. Other summaries, including
generation-local apply status, bootstrap summaries, and `katlctl` output, are
views only.

The event model should stay simple:

```text
request accepted
phase started
pre-exec mutation marker written
child process started
child process completed
evidence recorded
phase completed
operation failed
operation completed
startup audit classified stale record
```

Events must be append-only. Projections may be rebuilt from valid journal
events. If a projection is corrupt or stale, `katlc` should rebuild it or report
a recovery-required diagnostic rather than trusting it over the journal.

## Executor

Accepted operations execute inside the long-running `katlc` agent through an
internal executor. The executor may launch bounded child processes such as
`kubeadm`, `systemctl`, or systemd tooling when an operation contract requires
them.

Executor responsibilities:

```text
hold resource locks for protected mutating phases
write pre-exec mutation markers before mutating child processes
capture redacted stdout/stderr and exit status
record child process metadata such as pid and exit status
update operation journals and projections
stop or abandon timed-out work and classify the result
survive katlctl disconnects without cancelling accepted work
```

Executor metadata is agent-owned:

```text
agentStartID
executorAttemptID
childProcess
pid
exitStatus
startedAt
completedAt
```

Do not expose systemd invocation IDs or templated operation unit names as the
normal operation identity. Systemd journal references may be diagnostic
attachments when useful, but they are not the execution model.

## Startup Audit

On startup, `katlc` audits existing operation records before accepting new
mutating work. This is bookkeeping, not reconciliation toward a target state.

Startup audit may:

```text
rebuild record.json from valid journal events
classify interrupted records
finish idempotent Katl-owned host bookkeeping when the record proves it safe
record diagnostics for stale or ambiguous state
release locks that are safe to release after classification
```

Startup audit must not:

```text
rerun kubeadm
rerun kubectl
mutate etcd
refresh join material
continue multi-node rollout order
clean Kubernetes state
converge cluster membership
```

## Storage Backend

Day one should keep the journal-first file store unless a concrete requirement
forces a different backend. It is easy to inspect from rescue shells, aligns
with the generation files already under `/var/lib/katl`, and keeps bootstrap
failure debugging simple.

SQLite may become useful later for indexing, watches, and query projections, but
it should remain an implementation detail behind the same journal and API
contracts. Introducing SQLite must not change where authoritative operation
state lives conceptually, and it must not make recovery depend on an opaque
database when the node is half-booted.

## Not A Machine Controller

`katlc` is not a desired-state reconciler. It does not continuously compare a
cluster target against reality and autonomously converge it.

Allowed background work:

```text
serving the management API
agent health reporting
startup audit and safe bookkeeping
boot health/deadman checks
watch delivery for accepted operations
```

Disallowed background work:

```text
autonomous kubeadm init, join, upgrade, reset, or repair
autonomous Kubernetes API mutation
continuous cluster membership convergence
multi-node rollout continuation after katlctl exits
hidden cleanup of failed bootstrap or upgrade state
```

## Testing Contract

The agent architecture needs tests at the boundary where behavior becomes real:

```text
unit tests for command validation and lock decisions
unit tests for event append and projection rebuild
executor tests for child process failure, timeout, and redaction
startup audit tests for stale pre-mutation, host-only, post-mutation, and
  ambiguous records
API tests for SubmitOperation, GetOperation, WatchOperation, and disconnects
systemd-analyze verify for katlc-agent.service and supporting units
VM tests that drive katlctl from outside the node over the TCP gRPC path
```

Tests must prove the removed systemd re-exec operation path is not required for
normal bootstrap, apply, upgrade, or rollback workflows.
