# Kubernetes Sysext Delivery

Status: working design.

Katl needs a concrete path for Kubernetes payloads before the full day-2
upgrade controller exists. The north star is a set of Katl-produced, immutable
Kubernetes extension bundles for exact Kubernetes patch versions. Users and
automation reference those bundles by version and digest; Katl validates them
against the selected KatlOS runtime before creating or committing a generation.

## Decision

The durable artifact is a Kubernetes sysext plus metadata and catalog records.
It is not a Kubernetes distribution and it is not a user-specific node image.

Each published payload contains:

```text
katl-kubernetes-v<major>.<minor>.<patch>-<arch>.sysext.raw
katl-kubernetes-v<major>.<minor>.<patch>-<arch>.sysext.raw.sha256
katl-kubernetes-v<major>.<minor>.<patch>-<arch>.sysext.raw.json
kubernetes-sysext-catalog.json or an appendable catalog fragment
```

The sysext contains versioned Kubernetes host tools such as `kubeadm`,
`kubelet`, `kubectl`, and required helper packages. The sidecar metadata and
catalog entry bind artifact version, Kubernetes payload version, architecture,
package versions, source repository, digest, size, and supported Katl runtime
interfaces.

Generic confext content may be added to the bundle only when it is safe for
every node that selects that Kubernetes payload. Node-specific kubeadm input,
PKI, bootstrap tokens, kubeconfigs, network identity, secrets, and generated
Katl configuration remain node-local generated confext rendered by `katlc`.
Publishing prebuilt user-specific confext is outside the default path.

## Today's Install Story

Today, install and bootstrap use exact payload selection from the verified
KatlOS install image. The image bundles one or more Kubernetes sysext candidates
and their metadata. A user who wants Kubernetes `v1.36.1` installs a KatlOS
image that contains a `v1.36.1` Kubernetes sysext and sets
`node.bootstrap.kubernetesCatalogRef` to `v1.36.1`. Generation 0 stores that
intent but does not activate Kubernetes. The explicit bootstrap operation later
asks `katlc` to create generation 1, select the matching bundled sysext, render
node-specific generated confext, run kubeadm, and commit only after operation
health checks pass.

A user who wants a fresh cluster on Kubernetes `v1.36.2` uses a KatlOS install
image that bundles the `v1.36.2` sysext and sets
`node.bootstrap.kubernetesCatalogRef` to `v1.36.2`. That is a day-one fresh
install/bootstrap path once the artifact has been built and included in the
image.

Upgrading an already bootstrapped node from `v1.36.1` to `v1.36.2` is a
different workflow. The target sysext can be produced and cataloged now, but
node mutation remains unsupported until the kubeadm-aware upgrade operation and
kubelet activation gate are implemented and VM-tested.

## Producer Workflow

The first producer can live in this repository because the sysext currently
depends on Katl runtime compatibility metadata and the local mkosi build layout.
The workflow should be narrow:

```text
Renovate updates mkosi.profiles/kubernetes-sysext/kubernetes.env
  -> GitHub Actions builds the runtime base needed for compatibility metadata
  -> GitHub Actions builds the Kubernetes sysext for the exact target version
  -> checks verify sysext contents, metadata, package locks, and checksums
  -> katl-publish-kubernetes-sysext stages release-ready names and catalog data
  -> release assets or OCI artifacts are published immutably
```

Moving this producer to a separate repository is desirable once the artifact
contract is stable enough that the producer can consume Katl runtime interface
metadata without importing the whole KatlOS build tree. The split should happen
when it reduces release coupling, not before local VM and artifact validation are
reliable. A separate repo still needs to publish the same catalog schema and
must not weaken Katl runtime compatibility checks.

## Publication Target

GitHub release assets are the simplest first publishing target. They make the
artifact set inspectable, support immutable version tags when project policy
enforces no replacement, and match the current staged file names.

OCI distribution through GHCR is a strong follow-up when Katl needs registry
mirroring, retention policies, or digest-native fetch semantics. If OCI is used,
the OCI manifest digest becomes an additional distribution digest; the sysext
file SHA-256 remains the activation digest recorded in catalog and generation
metadata.

The catalog is authoritative for discovery, not for trust by itself. Consumers
must still verify the referenced sysext digest and, once signing is enabled,
verify the catalog or artifact signatures before staging or activation.

## Version Bumps

Kubernetes patch updates should be ordinary reviewed dependency updates.
Renovate should update the declared target payload and package expectations in
`mkosi.profiles/kubernetes-sysext/kubernetes.env` or its successor. That change
triggers the producer workflow, which builds a new immutable sysext artifact and
catalog entry. A successful `v1.36.2` publication does not replace `v1.36.1`;
both remain addressable by exact payload version and digest until retention
policy removes or deprecates them.

Minor updates, such as `v1.36` to `v1.37`, require the same artifact production
mechanics plus Kubernetes version-skew policy review. Katl should continue to
reject unsupported minor transitions on already bootstrapped nodes until the
kubeadm upgrade gate allows them.

## Deferred

The following remain separate backlog items:

```text
remote catalog fetch and node-local retention
artifact and catalog signing policy
release channel and deprecation policy
OCI/GHCR publication
separate producer repository split
kubeadm-aware Kubernetes upgrade execution
published generic confext supplements, if any are needed
```

