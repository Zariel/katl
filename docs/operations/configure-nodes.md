# Apply Cluster Configuration

Use `katlctl cluster apply` for supported configuration changes after
installation. The same `ClusterConfig` remains the source of truth for every
node and for kubeadm-owned Kubernetes configuration.

## Supported Input

The normal source is the same `ClusterConfig` used for installation. The current
renderer carries:

- SSH authorized keys;
- systemd-networkd files; and
- operation-only system role and role-dependent Kubernetes bootstrap state.

Runtime-safe fields apply normally. Katl coordinates affected node generations
and kubeadm phases internally. Disk/install selection and Kubernetes version
changes use the dedicated install and Kubernetes upgrade workflows.

If `spec.kubernetes.kubeadm` changes, cluster apply validates every node before
mutation and then reconciles every affected Kubernetes component online. A
Kubernetes configuration change never falls back to next-boot application or
requires a host reboot.

## Apply The Cluster

Apply the source configuration directly:

```sh
katlctl cluster apply --config ./cluster.yaml
```

Katl compiles every selected node configuration, validates the whole cluster,
and starts no mutation if any node rejects the plan. It then applies node
configuration and all affected Kubernetes component phases in a safe serial
order, returning only after the cluster is healthy.

If the source has already been compiled, pass the bundle through the same flag:

```sh
katlctl cluster apply --config ./katl-lab.katlcfg
```

Katl derives and verifies the bundle's integrity metadata from the file.

`katlctl` derives per-node generations, component phases, rollout ordering, and
operation identities internally. A successful return means the complete
supported configuration is active; partial or unsupported plans fail with the
node, field, and recovery action.

## Check Status

Use `katlctl node status cp-1 --config ./cluster.yaml` for the current healthy
generation. Use `katlctl operations list --config ./cluster.yaml --node cp-1`
when diagnosing an accepted or recently completed configuration operation.

On-node evidence remains available under:

```text
/var/lib/katl/generations/<generation>/
/var/lib/katl/operations/<operation-id>/
/var/lib/katl/boot/selection.json
```

If status reports rollback failure or `failed-needs-repair`, stop and follow the
reported recovery action before submitting another cluster apply.
