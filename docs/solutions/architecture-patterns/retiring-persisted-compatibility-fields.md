---
title: Retiring a persisted compatibility field against sum's supported rollback floor
date: 2026-09-24
category: architecture-patterns
module: returns
problem_type: architecture_pattern
component: data_model
severity: medium
applies_when:
  - "Removing or no longer writing a field in task records that older sum runtimes may read"
  - "Deciding which older runtimes still count as supported rollback targets"
  - "Proving mixed-version or rollback compatibility for a change to .sum state"
tags: [rollback, mixed-version, compatibility, task-record, notice, returns-sidecar]
related_components: [infrastructure, testing_framework]
---

# Retiring a persisted compatibility field against sum's supported rollback floor

## Context

Issue #210 retired the persisted `task.notice` mirror, a second copy of the latest delivery attempt that every pass rewrote into `task.json` beside the authoritative `returns.json` sidecar. The open question was the same one any retirement of persisted state faces: which older runtimes can still run against these records (by rollback, or side by side during an update), and do any of them depend on the field? Reading git history alone overstates the set. The floor is whatever the current updater will accept, and only the code says what that is.

## Guidance

1. **Derive the supported floor from current validation, not from history.**
   - Release targets: `release.VerifyRelease` requires every path in `requiredFiles` (`go/internal/release/release.go:30`), including `go/cmd/sumctl/main.go`, which first exists on main at the Go cutover (PR #114). Older trees cannot be release targets.
   - Checkout targets: `update apply` only fast-forwards, and checkout rollback refuses unless HEAD still equals the recorded SHA (`go/internal/updatecmd/activation.go:436`). A checkout target is therefore always the installation's current HEAD.
   - `Compatibility` (`go/internal/updatecmd/update.go:256`) checks only the state schema, the brief schema, and machine identity. `machineIdentityCompatibility` (`go/internal/updatecmd/identity.go:119`, from #213) refuses a target that predates the stable identity (#202) once records carry `m-` ids, unless `--allow-pre-machine-identity` is given. Treat pre-#202 targets as reachable, since hostname-only records and the override both exist.
   - In #210 this made every Python-era runtime unselectable, including the one (before the returns sidecar) that treated `task.notice` as its only delivery status.
2. **Classify each selectable runtime's readers as decision or display.** Run `git grep` on each boundary revision (the floor, the #202 change, the base) for the field. In #210 every reader from the floor on was display only (status rows, the duplicate `ask`, `Notify` output), and delivery decisions came from `returns.json`. Retire only display dependencies. If a selectable runtime decides anything from the field, keep writing it and record that runtime as the remaining dependency.
3. **Keep the key shape old readers subscript.** Keep writing the field once at record creation (`prepare` still writes `notice: null`). Never delete it from existing records. A historical value is harmless, and deleting it is a migration.
4. **Derive the public shape at the read boundary.** Use one projection over the authoritative record (`returns.Notice`/`NoticeOf`/`SetNotice` in `go/internal/returns/notice.go`), with a fallback to the historical field when the authority has nothing for that record.
5. **Prove it by running the old binaries.** `go/internal/cli/old_helpers_test.go` builds the floor (`supportedFloor`) and the merge base from git history, then runs each built binary against records the candidate wrote. It covers two histories: the older runtime created the installation and the candidate served it, and the candidate created everything. It skips the second history for pre-identity trees, since the updater refuses that rollback. CI checks out full history (`fetch-depth: 0` in `.github/workflows/verify.yml`), so this runs in the aggregate. In a shallow checkout it skips and names the missing revision.

## Why This Matters

Assuming too wide a floor keeps dead compatibility writes forever. In #210, the mirror cost one whole-record `task.json` rewrite (about 6.3 KB, since the record embeds the brief) per notified task per recorded outcome, measured with strace. Assuming too narrow a floor silently breaks a rollback the updater still allows. Grounding the floor in `VerifyRelease`, the activation check, and the identity guard gives a checkable answer that stays correct when those rules change.

## When to Apply

- Any change that stops writing, renames, or reshapes a field in `task.json`, `context.json`, session registrations, or a sidecar that an older runtime reads.
- Re-check the floor whenever `requiredFiles`, the checkout-rollback rule, or the `Compatibility` checks change, and update `supportedFloor` to match.

## Examples

Harness gotchas when running an older helper in the lab (all handled in `old_helpers_test.go`):

- Run it with the installation root as its working directory and `SUM_INSTALL_ROOT` set to that root. Otherwise it resolves the wrong installation.
- Older helpers reject `--format json`: Go helpers report `unknown flag: --format`, and the Python helper reports `invalid choice: 'json'`. Retry without the flag, since their default output is JSON.
- An older `init` records its own tree as the coordinator's cwd, so point the fake parent pane there.
- A pre-#202 runtime keys deliveries by the raw hostname. It reads a return that a newer runtime recorded under the stable key as pending and may prompt again. That comes from the delivery key, not from the retired field. Log it instead of failing on it, and remember that the updater refuses that rollback once records carry the stable identity.

## Related

- Issue #210, and #202 (stable machine identity), #213 (pre-identity rollback guard), #209 (per-recipient delivery locks).
- `docs/features/coordination.md` row `returns.notice-view`.
