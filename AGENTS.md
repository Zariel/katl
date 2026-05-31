# Agent Guidelines

Katl is a systemd-native Kubernetes node OS builder. Keep changes aligned with that boundary.

## Project Direction

- Treat `katlc` as the user-facing compiler for configuration, install assets, and update artifacts.
- Prefer Go for the main compiler, installer agent, node agent, and testable decision logic.
- Keep shell limited to small mkosi hooks and glue where a shell script is the clearest tool.
- Do not turn Katl into a Kubernetes distribution. Katl prepares kubeadm-ready nodes; kubeadm and user-managed GitOps take over from there.
- Do not hide native systemd configuration behind a lossy abstraction. Convenience config must compile to native artifacts and allow passthrough.

## Runtime Model

- Target modern systemd primitives: systemd-boot, UKIs, systemd-repart, systemd-sysext, systemd-confext, systemd-tmpfiles, systemd mount units, and systemd health/boot-complete semantics.
- Assume EFI-only boot unless a design document explicitly expands scope.
- Keep the runtime root immutable and versioned. Persistent Kubernetes and node state belongs under writable state partitions and should be projected into expected paths with systemd mount units or bind mounts.
- Do not store persistent identity in `/run`; `/run` is for ephemeral runtime state.

## Testing Expectations

- Design installer behavior as typed, idempotent, and testable state transitions.
- Prefer unit tests for planning and validation logic, golden tests for generated assets, and QEMU/libvirt tests for boot/install/update flows.
- Generated systemd units should be verifiable with `systemd-analyze verify` where practical.
- Changes to disk layout, boot flow, update flow, or kubeadm state handling need tests or an explicit note explaining the remaining gap.

## Task Tracking

- Use Beads through the `bd` CLI for project task tracking.
- Check `bd ready` or `bd list` before starting new work.
- Create tasks with concrete acceptance criteria when adding non-trivial implementation work.
- Close or update Beads when the related code/docs change is completed.
- Keep Beads operational data local unless the project explicitly decides to publish or sync the database.

## Git Workflow

- Review `git status --short` before editing, staging, or committing.
- Only stage and commit files that are part of the current task. Do not sweep unrelated local changes into a commit.
- Prefer explicit path staging, for example `git add AGENTS.md docs/internal/initial-design.md`.
- Use `git commit-wrapped` for commits so commit messages go through the project wrapper.
- Do not rewrite history, reset, or discard user changes unless the user explicitly asks for that operation.
- If unrelated work is present in the tree, leave it alone and mention it in the handoff if it affects verification.

## Completion Gates

- Before closing a Bead, run the validation gates that match the change: formatting, unit tests, generated asset checks, `systemd-analyze verify`, QEMU/libvirt smoke tests, or docs review as applicable.
- For broad, risky, security-sensitive, boot/update, disk-layout, or kubeadm-state changes, request or run a subagent/code review before closing the Bead.
- Use Beads gates when work depends on external review, long-running validation, or serialized merge coordination.
- Record skipped gates in the Bead or final handoff with the reason.

## Documentation

- Keep examples using Katl naming: `katl`, `katlc`, `katlctl`, `/etc/katl`, `/var/lib/katl`, and `katl.*` kernel arguments.
- Update `docs/internal/initial-design.md` when architectural decisions change.
- Record unresolved design choices as open questions instead of burying uncertainty in examples.
