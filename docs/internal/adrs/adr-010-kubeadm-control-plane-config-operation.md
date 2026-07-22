# ADR-010: Kubeadm configuration changes use component-scoped live operations

Status: accepted.

Date: 2026-07-11; expanded 2026-07-22.

## Context

Katl compiles default or bounded operator-supplied native kubeadm input into
role-dependent state under `/etc/katl/kubeadm/<name>/config.yaml`, but normal
generation activation must not rewrite kubeadm-owned state. A running control plane has derived state in
the `kube-system/kubeadm-config` ConfigMap and in static Pod manifests under
`/etc/kubernetes/manifests`. Changing the desired file is therefore not the
same action as changing the live cluster.

Kubeadm has no generic reconfiguration transaction. It exposes individual
phases. In Kubernetes v1.36, `kubeadm init phase control-plane all --config`
generates the API server, controller manager, and scheduler static Pod
manifests, while `kubeadm init phase upload-config kubeadm --config` uploads the
`ClusterConfiguration` used by later join, reset, and upgrade operations.
Those phases can be composed safely only for a bounded field set and a serial
multi-node rollout.

Katl must also apply cluster-wide `KubeletConfiguration` changes without a
reinstall. This uses a different kubeadm phase sequence and restarts kubelet one
node at a time. A single arbitrary-YAML transaction would hide those different
mutation and recovery boundaries.

Upstream command references used by this decision:

- <https://kubernetes.io/docs/reference/setup-tools/kubeadm/generated/kubeadm_init/kubeadm_init_phase_control-plane_all/>
- <https://kubernetes.io/docs/reference/setup-tools/kubeadm/generated/kubeadm_init/kubeadm_init_phase_upload-config_kubeadm/>
- <https://kubernetes.io/docs/reference/setup-tools/kubeadm/kubeadm-config/>
- <https://kubernetes.io/docs/tasks/administer-cluster/kubeadm/kubeadm-reconfigure/>
- <https://kubernetes.io/docs/reference/command-line-tools-reference/kube-apiserver/>
- <https://kubernetes.io/docs/reference/command-line-tools-reference/kube-controller-manager/>
- <https://kubernetes.io/docs/reference/command-line-tools-reference/kube-scheduler/>

## Decision

Katl reconciles kubeadm-owned state as part of the same cluster-wide config
apply that reconciles KatlOS node configuration. Component scopes are internal
execution phases, not operator-selected commands.

Cluster apply activates a generation containing changed desired kubeadm input
live. Generation activation first changes the selected immutable input
under `/etc/katl/kubeadm`; it records `kubeadm action required` and never invokes
kubeadm or writes kubeadm-owned live state. After refreshing the confext overlay,
Katl uses `systemctl try-restart kubelet.service` so an already-active kubelet
rebinds its static-pod watcher to the restored `/etc/kubernetes` mount. This is a
no-op before kubelet is active. Cluster apply then runs every affected
kubeadm-aware phase online before it reports success.
Live generation activation refreshes the systemd confext overlay after moving
the generation link, and does the same after a pre-mutation rollback. A
generation is not reported active-live while `/etc` still exposes the previous
confext contents.

The internal operation supports three component phases:

- `control-plane`: changes to `extraArgs`, `extraEnvs`, or `extraVolumes` under
  `apiServer`, `controllerManager`, or `scheduler`. Katl regenerates only the
  affected static Pod when one component changed, or all three when several
  changed, then uploads the shared `ClusterConfiguration` after the serial
  rollout.
- `kubelet`: a cluster-wide `KubeletConfiguration`. Katl uploads the desired
  configuration once from a control-plane coordinator, downloads it through
  `kubeadm upgrade node phase kubelet-config` on each intended node, restarts
  kubelet, and verifies that the resulting local config contains the desired
  fields.
- `kube-proxy`: a cluster-wide `KubeProxyConfiguration`. The control-plane
  coordinator runs the bounded kubeadm addon phase, verifies the resulting
  ConfigMap, and waits for the kube-proxy DaemonSet rollout online.

The selected kubeadm API must be `kubeadm.k8s.io/v1beta4`. The active
Kubernetes payload version and digest remain unchanged for this operation. A
request that also changes the Kubernetes payload must use `kubeadm-upgrade` and
cannot be combined with this operation.

## Source Of Truth

Desired state is the internally selected, generated kubeadm config in one
committed Katl generation:

```text
desired generation ID
selected KubeadmConfig name
/etc/katl/kubeadm/<name>/config.yaml
canonical desired config SHA-256
```

The name is compiled from the node role derived from `controlPlane`; it is not
an operator-authored ClusterConfig reference. When `spec.kubernetes.kubeadm` is
present, the desired digest includes its validated and role-selected native
documents and patches.

The operation accepts only a config selected by the active generation. It does
not accept an arbitrary path or inline replacement YAML. Every participating
node must select the role-appropriate config, and all nodes in one component
rollout must agree on that component's cluster-wide desired document.

Live state is collected read-only from:

```text
kube-system/kubeadm-config ConfigMap
kube-system/kubelet-config ConfigMap
/etc/kubernetes/manifests/kube-apiserver.yaml
/etc/kubernetes/manifests/kube-controller-manager.yaml
/etc/kubernetes/manifests/kube-scheduler.yaml
active Kubernetes sysext metadata
running static Pod and component health
/var/lib/kubelet/config.yaml and kubelet health
```

Katl canonicalizes the selected desired document and the relevant live state.
Control-plane planning reports changed component field groups. Kubelet
verification treats kubeadm-defaulted live fields as additional state but
requires every explicitly desired field and value. Unsupported differences are
`unsupported/manual`, not a partial apply.

Before a control-plane phase runs, Katl writes an operation-private effective
kubeadm input. It starts from the live `ClusterConfiguration`, preserving
runtime-derived values such as the stable endpoint, cluster networking, and
certificate state, then overlays only the supported desired component fields.
The immutable generated input remains the desired source of truth; the
effective file is bounded execution material and is never activated as node
configuration.

## Validation And Refusal Rules

Before accepting a rollout, `katlctl` and node-local `katlc` validate:

```text
at least one intended control-plane node is present
all nodes agree on cluster identity and stable control-plane endpoint
all nodes run the same Kubernetes payload version and digest
the desired generation is active and committed on every target node
the selected KubeadmConfig name and cluster-wide digest agree on every node
katlc derives the live kubeadm ConfigMap identity immediately before mutation
the internally observed desired/live differences belong to the selected component
all intended nodes are Ready and the stable API endpoint is healthy
all stacked-etcd members are healthy and voting before control-plane mutation
no concurrent kubeadm-state operation owns a target node
stacked-etcd and API health pass before and after each bounded node mutation
```

The operation refuses:

```text
controlPlaneEndpoint, networking, kubernetesVersion, certificates, certSANs,
  certificate directories, image repository, DNS, proxy, encryption, or
  feature-gate changes
any etcd local/external setting or etcd static Pod patch
control-plane fields outside extraArgs, extraEnvs, and extraVolumes
arbitrary static Pod patches or denied host-path changes
InitConfiguration or JoinConfiguration changes after bootstrap
adding, removing, or replacing a control-plane or etcd member
combined Kubernetes payload and control-plane configuration changes
parallel node mutation, unknown quorum, an unhealthy API, or desired state that
  changed after operation acceptance
```

Katl may add further component scopes only with an explicit phase, failure
contract, and VM gate. Native kubeadm passthrough remains available as desired
input, but passthrough does not imply live-operation support.

## Plan And Command Surface

The only supported operator entry point is:

```text
katlctl cluster apply --config cluster.yaml
```

Katl renders the complete desired configuration for every node, validates the
whole cluster before mutation, activates each node's desired generation, and
then runs all affected Kubernetes component phases in dependency order. A
Kubernetes configuration change is online-only: it is never accepted as a
next-boot change and never asks the operator to reboot. If a field lacks a safe
online reconciler, preflight rejects the whole cluster plan before mutation and
names that field.

Planning is read-only. The explicit plan reports:

```text
config name and desired generation
internally derived desired and live canonical identities
internally observed supported field-level delta
active Kubernetes payload version and identity
component-specific node order and coordinator
etcd member and API health preconditions
static manifest digests before mutation
commands that would run
unsupported differences and required manual action
```

A no-change plan exits distinctly from action-required, unsupported/manual,
and collection failure. Planning never writes `/etc/kubernetes`, calls a
mutating kubeadm phase, changes a ConfigMap, restarts kubelet, or changes a
generation.

Execution uses the kubeadm binary from the active Kubernetes sysext. Target
kubeadm private-mount access is unnecessary because the Kubernetes version is
unchanged.

For a control-plane component, the node-local mutating phase is:

```text
kubeadm init phase control-plane <apiserver|controller-manager|scheduler|all> \
  --config /var/lib/katl/operations/<operation>/effective-kubeadm.yaml
```

After every node succeeds, the coordinator runs once:

```text
kubeadm init phase upload-config kubeadm \
  --config /var/lib/katl/operations/<operation>/effective-kubeadm.yaml
```

For kubelet configuration, the coordinator first runs
`kubeadm init phase upload-config kubelet --config ...`. Each node then runs
`kubeadm upgrade node phase kubelet-config --config ...` followed by
`systemctl restart kubelet.service`. Every supported kubeadm phase is dry-run
before the first mutation on that node.

Katl does not run the `etcd local`, certificate, kubeconfig, CoreDNS addon, or
bootstrap-token phases for this operation.

## Rollout Ordering

`katlctl` is the rollout coordinator; each node's `katlc` remains the authority
for its local mutation and `OperationRecord`. The explicit inventory identifies
one coordinator. For control-plane configuration the coordinator is changed
last:

```text
1. acquire the rollout plan and verify all cluster-wide preconditions
2. create and verify the required etcd snapshot evidence
3. mutate the first non-coordinator control plane
4. wait for the affected static Pods, local API, node Ready state, stable API VIP,
   and etcd health
5. mutate the second non-coordinator and repeat every health check
6. mutate the coordinator and repeat every health check
7. upload the shared kubeadm ClusterConfiguration from the coordinator
8. verify the live ConfigMap digest, manifest digests, all intended Ready nodes,
   stable API VIP, and healthy etcd members
```

Only one node may own the mutating phase at a time. The coordinator stops at the
first refusal, timeout, failed command, or failed health check. It never moves
to the next node after an ambiguous result.

Katl cordons a node before a control-plane manifest mutation and restores its
original schedulability only after local and cluster health pass. It does not
drain or reboot the node.

Kubelet configuration starts at the coordinator so the shared ConfigMap is
uploaded before any other node downloads it. Nodes are then updated serially;
each local config backup, restart, Ready transition, and post-health result is
recorded before proceeding.

Reconciliation is idempotent. A control-plane node whose supported fields
already match performs no kubeadm phase or cordon. A node whose local kubelet
config already contains the desired fields is not restarted, and the
coordinator uploads the shared kubelet ConfigMap only when its canonical
configuration differs.

Kube-proxy configuration runs once on the coordinator. Katl performs the
kubeadm addon dry-run, applies the desired ConfigMap and DaemonSet, and waits
for the DaemonSet rollout. It does not reboot a host or restart kubelet.

## Etcd And Static Pod Safety

The operation never regenerates `etcd.yaml`, changes `/var/lib/etcd`, changes
membership, or runs an etcd mutation. Etcd health and snapshot evidence are
preconditions because the operation changes API-serving static Pods and later
updates a ConfigMap stored in etcd.

The snapshot is recovery evidence, not permission for automatic restore. Katl
does not restore it, remove a member, or claim etcd rollback after a failed
control-plane configuration operation.

Static manifest backups and SHA-256 values are retained in the restricted
operation directory before mutation. They are diagnostic and explicit-repair
inputs. The normal host-generation rollback path must not copy them back or
claim that live kubeadm state was restored.

## Online, Generation, And Rollback Semantics

This is an online live-state operation and does not require a reboot. The
operator command first activates a kubeadm-input-only generation when desired
input changed; the node-local kubeadm operation itself requires that committed
generation and never changes generation selection.

Normal `katlc apply` may render and select a different desired kubeadm config,
but it only records `action-required`. It must not invoke kubeadm, kubectl,
crictl, restart kubelet, write `/etc/kubernetes`, or mutate Kubernetes objects.

Rolling a Katl generation backward changes desired input only. It does not
reverse static manifests or the kubeadm ConfigMap. Reversing a completed live
change requires a new explicit `kubeadm-control-plane-config` rollout whose
desired and expected-live digests describe that reverse transition. There is no
automatic post-mutation rollback.

Failure before the first pre-exec mutation marker abandons the attempt without
claiming live change. Failure after any manifest write or ConfigMap upload stops
the rollout and sets `recoveryRequired`; host rollback is not Kubernetes repair.
The status names the last proven healthy node, the uncertain or failed node,
the manifest and ConfigMap digests observed, and the exact retry or reverse
operation required.

## Operation Records

Each node writes an `OperationRecord` with:

```text
operationKind: kubeadm-control-plane-config
internal component phase: control-plane, kubelet, or kube-proxy
scope and resource lock: kubeadm-state
rollout ID, node position, node count, and coordinator identity
actor, expected machine ID, cluster identity, and stable endpoint
desired generation ID and selected KubeadmConfig name
desired config path and canonical SHA-256
expected and observed live kubeadm ConfigMap SHA-256
active Kubernetes payload version and digest
supported field-level delta
snapshot reference, digest, revision, member-list digest, source etcd version,
  creation time, storage location, and operator identity
original node schedulability
before/after SHA-256 for all three control-plane manifests
before/after SHA-256 for local kubelet config when selected
before/after static Pod container identities and component health
API VIP, node Ready, and etcd member/endpoint health evidence
pre-exec mutation markers and redacted kubeadm invocations
whether the coordinator ConfigMap upload ran
terminal result, recoveryRequired, and next action
```

The control-plane phase plan is:

```text
accepted
preflight-complete
cordon-complete
manifest-backup-complete
control-plane-manifests-running
control-plane-manifests-complete
post-manifest-health-complete
uncordon-complete
operation-complete
```

The coordinator record additionally contains:

```text
rollout-members-verified
kubeadm-config-upload-running
kubeadm-config-upload-complete
post-upload-health-complete
```

The kubelet phase plan replaces the manifest phases with kubelet config backup,
optional coordinator upload, local kubelet-config download, kubelet restart,
and post-kubelet health phases.

The public `katlctl` summary reports node outcomes without exposing operation
IDs or integrity plumbing. Node-local journals retain internally derived
request, desired-state, live-state, payload, and manifest identities and remain
the mutation authority.

## Consequences

Operators can change kubeadm-owned control-plane manifest settings and
cluster-wide kubelet settings from the same ClusterConfig without reinstalling
or rebooting nodes. Unsupported desired input is reported precisely instead of
being silently or partially applied.

The coordinator and node-local executor are separate responsibilities. A
client interruption cannot make `katlc` forget a local mutation, and a node
failure cannot cause the coordinator to continue to another control plane.

Adding future fields requires deciding whether existing kubeadm phases remain
safe, whether certificates or etcd are involved, what health proof is needed,
and how a reverse operation works.

## Rejected Alternatives

Applying every `ClusterConfiguration` difference was rejected because kubeadm
fields have different ownership, restart, certificate, etcd, and rollback
semantics.

Treating `/etc/kubernetes/manifests` as confext output was rejected because the
files are persistent kubeadm-owned live state and must remain writable by
kubeadm.

Running the change during normal generation activation was rejected because a
boot or desired-input rollback must not hide a cluster mutation.

Regenerating the etcd manifest with the control-plane manifests was rejected
because this operation neither changes etcd configuration nor owns etcd
recovery.

Parallel control-plane mutation was rejected because it can remove API
capacity, disrupt controller leadership, and make failure ownership ambiguous.

Automatic manifest or snapshot rollback was rejected because an interrupted
static-Pod rewrite is external mutation and restoring a snapshot is a distinct
cluster-wide recovery operation.
