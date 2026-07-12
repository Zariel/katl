# Apply KatlOS Node Configuration

Use config apply for supported host configuration after installation. It is not
a general Kubernetes, disk, kubeadm, or operating-system upgrade mechanism.

## Supported Input

The normal source is the same `ClusterConfig` used for installation. The current
renderer carries:

- hostname;
- SSH authorized keys; and
- systemd-networkd files.

It excludes disk/install policy, system role, Kubernetes bundle selection, and
kubeadm lifecycle state. A desired kubeadm input may be rendered and recorded as
requiring a later explicit action; config apply does not run kubeadm.

## Render and Review

Use a monotonically increasing desired version for this source:

```sh
katlctl config render-node \
  --source ./cluster.yaml \
  --node cp-1 \
  --desired-version 2 > cp-1.runtime.yaml
```

Review the rendered files before contacting the node. Do not place private
keys, bearer tokens, or other secret values in source configuration.

## Validate Through the Node

```sh
katlctl config apply validate \
  --endpoint cp-1.example.test:9443 \
  --agent-token-file ./tokens/cp-1.token \
  --source ./cluster.yaml \
  --node cp-1 \
  --desired-version 2 \
  --candidate-generation config-2
```

Validation reports the changed domains and accepted apply mode without
accepting an operation. The default `auto` lets the domain policy select live or
next-boot application. Request `--mode live` or `--mode next-boot` only when you
intend to constrain that policy; unsafe requests are refused.

If the source has already been compiled, replace `--source` with:

```text
--config-bundle ./katl-lab.katlcfg
```

Katl derives and verifies the bundle's integrity metadata from the file.

## Apply the Reviewed Request

Run the same arguments without the `validate` subcommand:

```sh
katlctl config apply \
  --endpoint cp-1.example.test:9443 \
  --agent-token-file ./tokens/cp-1.token \
  --source ./cluster.yaml \
  --node cp-1 \
  --desired-version 2 \
  --candidate-generation config-2
```

Keep the configuration inputs identical to the reviewed plan. `katlctl`
generates the retry identity, follows the durable apply, and exits only after a
terminal result. Require `terminal: true` and `result: succeeded`. If
`recoveryRequired` is true, stop and follow `failureReason` and `nextAction`.

## Check Generation Status

Query the candidate through the agent:

```sh
katlctl config apply status \
  --endpoint cp-1.example.test:9443 \
  --agent-token-file ./tokens/cp-1.token \
  --generation config-2
```

For a live change, require committed state and healthy config-apply evidence.
For a next-boot change, require committed staged state, reboot in a controlled
window, then require the candidate to become healthy after
`katl-boot-complete.target`.

On-node evidence remains available under:

```text
/var/lib/katl/generations/<generation>/
/var/lib/katl/operations/<operation-id>/
/var/lib/katl/boot/selection.json
```

If status reports rollback failure, `failed-needs-repair`, or a kubeadm action
requirement, stop and classify it before submitting another configuration.
