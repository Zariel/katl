# Writable State Partition Layout

This decision defines the first filesystem layout for the Katl writable state
partition mounted at `/var`.

## Decision

Katl creates one root-disk state partition with GPT label `KATL_STATE`, type
`var`, and filesystem `ext4` for the first implementation. The installer and
boot metadata should prefer stable partition identity in this order:

```text
PARTUUID recorded from the installed state partition
GPT label KATL_STATE as a local validation hint
systemd-gpt-auto type var only when the target disk is unambiguous
```

Persistent identity must not be stored in `/run`. `/run` is only for boot-local
activation links and service handoff state that can be regenerated from `/var`.

## Etcd Data Placement

The first supported path keeps etcd data at `/var/lib/etcd` on the `KATL_STATE`
partition mounted at `/var`. Kubeadm or the etcd static pod owns the directory
contents. Katl only guarantees that `/var` is mounted before kubeadm-managed
control-plane services need the path. This keeps worker nodes, single-node
experiments, and control-plane nodes on the same base install layout until the
installer has role-aware storage policy.

A dedicated etcd data partition is a future Katl-owned root-disk partition, not
an `extraDisks` mount. When exposed in the install manifest, it should be a
first-class field under install storage, for example:

```json
{
  "install": {
    "storage": {
      "etcd": {
        "dedicatedPartition": {
          "sizeMiB": 16384,
          "filesystem": "ext4"
        }
      }
    }
  }
}
```

That request means: carve a `KATL_ETCD` partition from the selected target root
disk, format it with the requested filesystem, and mount it at `/var/lib/etcd`.
It must reduce the remaining `KATL_STATE` size and fail planning if the target
disk cannot still satisfy both root slots and the minimum state partition size.
The initial filesystem should be `ext4`; adding `xfs` support needs explicit
validation and mount-unit coverage.

Unsafe cases must be rejected rather than interpreted as etcd storage:

- `install.extraDisks[].mount.path` equal to `/var/lib/etcd` or any parent or
  child path that would shadow it.
- Extra-disk selectors that resolve to the selected target root disk or one of
  its partitions.
- Dedicated etcd partition requests without a positive size, with a filesystem
  outside the supported allowlist, or that leave less than the minimum state
  partition size.
- Any attempt to store etcd under `/run`, `/etc`, `/usr`, `/tmp`, kubelet state,
  containerd state, or Katl generation metadata.

## Required Directories

`katlos-install` or first-boot tmpfiles rules must ensure these directories
exist on the state partition:

| Directory | Owner | Mode | Purpose |
| --- | --- | --- | --- |
| `/var/lib/katl` | `root:root` | `0755` | Katl persistent state root |
| `/var/lib/katl/generations` | `root:root` | `0755` | Per-generation records, staged extension content, and boot status |
| `/var/lib/katl/generations/<id>` | `root:root` | `0755` | One root/sysext/confext generation |
| `/var/lib/katl/generations/<id>/metadata.json` | `root:root` | `0644` | Generation selection plus mutable boot/health status fields |
| `/var/lib/katl/generations/<id>/confext` | `root:root` | `0755` | Generated confext tree or image for the generation |
| `/var/lib/katl/generations/<id>/sysext` | `root:root` | `0755` | Sysext artifacts selected with the generation |
| `/var/lib/katl/identity` | `root:root` | `0755` | Stable machine identity backing files |
| `/var/lib/katl/identity/machine-id` | `root:root` | `0444` | Random install-generated systemd machine ID backing file |
| `/var/lib/katl/kubernetes` | `root:root` | `0755` | Kubernetes projected state namespace |
| `/var/lib/katl/kubernetes/etc-kubernetes` | `root:root` | `0755` | Backing store for projected `/etc/kubernetes` |
| `/var/lib/katl/ssh` | `root:root` | `0755` | SSH projected state namespace |
| `/var/lib/katl/ssh/host-keys` | `root:root` | `0700` | Backing store for persistent SSH host keys |
| `/var/lib/kubelet` | `root:root` | created by package/tmpfiles | Kubelet native persistent state |
| `/var/lib/containerd` | `root:root` | created by package/tmpfiles | Containerd native persistent state |
| `/var/lib/etcd` | `root:root` | created by kubeadm/etcd or mount | Etcd data when not using a dedicated etcd partition |
| `/var/log/journal` | `root:systemd-journal` | created by systemd-journald | Persistent journal, only when enabled |

Generation content is immutable after creation except through explicit repair
tooling. In the first metadata schema, `metadata.json` also carries mutable
`bootState` and `healthState` fields. Those status fields may be updated by boot
health, rollback, or repair tooling; root slot, UKI, sysext, and confext
selection fields must not be changed in place. Mutable pointers such as
"current" should not live inside an individual generation directory.

`katlos-install` creates `/var/lib/katl/identity/machine-id` with a random
machine ID during install. It is stable across normal boots and updates because
it lives on the state partition, but it is not deterministic and does not need
to survive a destructive reinstall. The backing file should be root-owned and
write-protected after install.

## Activation State

At boot, Katl may create these ephemeral paths from generation metadata:

```text
/run/extensions/<selected sysext>
/run/confexts/<selected confext>
```

These are not persistent state. They must be recreated every boot after `/var`
is mounted and before `systemd-sysext.service` or `systemd-confext.service`
runs.

## Directories Left To Systemd Or Packages

Katl should not pre-create every application-owned subdirectory. These paths are
left to package defaults, tmpfiles, or the owning service unless a later task
finds an ordering problem:

```text
/var/cache
/var/lib/cni
/var/lib/containers
/var/lib/private
/var/log
/var/tmp
```

Kubelet and containerd package tmpfiles may create deeper subdirectories below
their state roots. Katl's responsibility is that `/var` is mounted and the
top-level persistent view is available before those services start.

The `/etc/kubernetes` projection from
`/var/lib/katl/kubernetes/etc-kubernetes` is defined in
`docs/internal/etc-kubernetes-projection.md`.

## Follow-up Gates

Mount units and tmpfiles snippets should be verified with `systemd-analyze
verify` once they exist. QEMU boot tests must prove that `/var` is mounted by
stable partition identity and that no persistent identity is read from `/run`.
