# Upgrade a KatlOS Host

KatlOS host upgrades are one-node-at-a-time operations. The normal command
resolves a release, stages its root and UKI into the inactive slot, reboots into
a bounded trial, and waits for boot health. It does not upgrade Kubernetes or
orchestrate availability across several hosts.

## Preconditions

- the node is healthy on a known-good generation;
- no other mutating node operation is active;
- the selected upgrade SquashFS is from the intended KatlOS release;
- the upgrade declares a compatible architecture and runtime interface;
- the node can fetch release artifacts from GitHub;
- the installation `ClusterConfig` contains the node and its current management
  address, or `--endpoint` supplies an override;
- the command is run during the intended reboot window; and
- Kubernetes and workload availability have been handled outside Katl.

## Plan

```sh
katlctl node upgrade v2026.7.0-beta.1 cp-1 --config ./cluster.yaml --plan
```

A plan response has no durable mutation and does not reboot the node.

During staging, the node downloads or opens the image, calculates its SHA-256
and size, records that resolved identity in the operation, and checks the image's
component metadata before changing the inactive slot.

## Upgrade

Run the command without `--plan`:

```sh
katlctl node upgrade v2026.7.0-beta.1 cp-1 --config ./cluster.yaml
```

For repeated day-two commands, `katlctl context save --config ./cluster.yaml`
can save this topology locally. That context is optional; it is not a second
cluster configuration operators must maintain.

`katlctl` follows staging progress, asks the node agent to reboot,
waits for the agent to restart, and requires the selected generation to be
committed, booted, and healthy. The default result is concise text; use
`--output json` when automation needs the structured `rebooted` and
`bootHealth` fields. Check workload availability before upgrading another host.

During the reboot, the console may show a containerd stop-job
countdown after the containerd daemon has exited. Containerd deliberately keeps
its shim processes across ordinary daemon restarts; a full host shutdown lets
systemd finish the remaining workload and shim cleanup. Allow that shutdown to
complete rather than forcing power off, which can increase the risk of
workload data loss. If it repeatedly reaches the systemd timeout, preserve the
previous-boot journal before retrying the upgrade.

## Failure Boundary

Boot health may select the previous known-good host generation. `katlctl`
reports that rollback and stops. It does not
undo Kubernetes, etcd, workload, or external-infrastructure changes. If the
operation record says `recoveryRequired: true`, or the node fails to return,
stop the rollout and collect the evidence in [Troubleshoot KatlOS](troubleshoot.md).
