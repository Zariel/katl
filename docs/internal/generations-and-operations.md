# Katl Generations And Operations

Status: working design.

This document defines the shared lifecycle model for Katl node state and
stateful actions.

## Summary

Katl models node lifecycle through two complementary concepts:

```text
Generations
  Declarative, versioned, rollback-aware desired host state.

Operations
  Explicit, auditable, transactional actions required to make reality match
  desired state.
```

This separation lets Katl stay systemd-native and generation-based without
building a Talos-style machine controller or reimplementing Kubernetes lifecycle
management.

## Motivation

Katl uses systemd-native mechanisms such as systemd-boot, sysext, confext,
mount units, boot health checks, and generation activation to manage host
state. These mechanisms work well for operating system configuration, installed
capabilities, and node-level services.

Some lifecycle transitions are different. Kubernetes bootstrap, node join,
certificate renewal, control-plane repair, etcd membership changes, and
Kubernetes version upgrades mutate persistent node or cluster state through
tools such as kubeadm. They cannot safely be treated as simple configuration
changes or hidden inside confext activation.

Katl therefore keeps declarative host state in generations and models
transactional workflows as operations.

## Generations

A generation describes the desired state of a node.

Examples include:

```text
KatlOS runtime version
kernel and boot artifacts
enabled sysexts
rendered confext configuration
selected Kubernetes sysext version
host networking configuration
container runtime configuration
node role and capabilities
health expectations
```

A generation answers:

```text
What should this machine look like?
```

Generations are versioned, health-checked, and rollback-aware. Rollback selects
a complete previous generation; it must not independently switch only the root
slot, sysext set, or confext set.

The initial installed baseline is generation 0. `katlos-install` creates it
after validating the install request, writing the runtime root, installing boot
artifacts, preparing writable state, and seeding enough systemd wiring for the
installed runtime to accept node-local operations.

Generation 0 is intentionally not a Kubernetes cluster member. It is the
post-install KatlOS baseline.

## Operations

An operation represents a stateful workflow required to transition the node,
host capability set, or Kubernetes cluster state.

Examples include:

```text
PrepareKubernetes
BootstrapCluster
JoinCluster
UpgradeControlPlane
UpgradeWorker
RenewCertificates
ResetNode
ReplaceEtcdMember
```

An operation answers:

```text
What action must occur to reach the desired state?
```

Operations are explicit. Normal configuration apply and generation activation
must not silently run kubeadm, kubectl, CNI installers, GitOps controllers,
package managers, or cluster repair commands.

## Command And System Boundaries

`katlc` is the node-local authority. It validates node-local input, compiles or
selects candidate generations, plans operation records, launches
systemd-supervised operation units, and records node-local status.

`katlctl` is the operator UX and remote or multi-node orchestration layer. It
may connect to installed nodes, submit explicit operation requests, coordinate
bootstrap or rolling upgrade order, and report cluster-level progress.

Systemd executes and supervises node-local operations. It owns unit ordering,
dependency management, restart handling, logging, health targets, and boot
success tracking.

Kubeadm remains authoritative for Kubernetes cluster mutation. It owns
bootstrap, join workflows, control-plane upgrades, node upgrades, kubelet
configuration updates, certificate behavior, and kubeadm-managed Kubernetes
objects.

Katl owns the boundary around those tools:

```text
host state
generation management
configuration rendering
operation planning
operation status and diagnostics
health verification
host rollback decisions
```

## Lifecycle Model

The installer creates generation 0:

```text
Install KatlOS
  -> create generation 0
  -> boot generation 0
  -> reach installed-runtime health
```

Kubernetes host preparation is the first post-install operation:

```text
PrepareKubernetes
  -> select Kubernetes sysext
  -> render kubeadm-ready configuration
  -> project /etc/kubernetes from writable state
  -> verify containerd, kubelet wiring, and kubeadm tools
  -> create generation 1
  -> mark generation 1 healthy after local checks pass
```

Generation 1 is kubeadm-ready host state. It has not run `kubeadm init` or
`kubeadm join`.

Cluster bootstrap and node join are later explicit operations:

```text
BootstrapCluster
  -> run kubeadm init
  -> verify local control-plane health
  -> publish bootstrap artifacts
  -> mark operation complete

JoinCluster
  -> run kubeadm join
  -> verify node-local join health
  -> mark operation complete
```

Kubernetes upgrades use the same pattern after bootstrap:

```text
Generation N
  Kubernetes 1.36.0

Generation N+1
  Kubernetes 1.36.1

UpgradeControlPlane or UpgradeWorker
  -> run kubeadm upgrade apply or kubeadm upgrade node
  -> restart kubelet at the planned point
  -> verify local health
  -> mark generation N+1 healthy
```

## Failure And Rollback

Generations provide host rollback. Operations provide transactional status and
repair context.

Before an operation mutates external or Kubernetes cluster state, a failed
candidate generation can usually be abandoned and the node can return to the
previous known-good generation.

After an operation mutates Kubernetes cluster state, host rollback must not
claim to undo that mutation. For example, rolling back from a target Kubernetes
sysext to a previous host generation does not necessarily roll back kubeadm
changes already written to `/etc/kubernetes`, kubelet state, etcd, or
Kubernetes API objects.

Operation status must therefore record:

```text
previous generation id
candidate generation id, when one exists
operation phase
whether kubeadm or another mutating tool has run
diagnostic artifact paths
whether host rollback was attempted
whether kubeadm-aware repair or retry is required
```

This keeps host state declarative and rollback-aware while acknowledging that
some operations are inherently transactional.

## Testing Contract

The operation model needs tests at the level where behavior becomes concrete:

```text
unit tests for operation planning and validation
golden tests for generated operation records and systemd units
systemd-analyze verify for generated units where practical
VM tests for install, PrepareKubernetes, bootstrap, join, upgrade, rollback,
  and repair workflows as they are implemented
```

Documentation-only changes to this model do not require VM gates. Any
implementation that changes boot, install, update, kubeadm, or operation
execution behavior needs the relevant VM gate or an explicit recorded host
capability gap.
