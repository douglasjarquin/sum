# Dependencies and compatibility

[`dependency-inventory.json`](dependency-inventory.json) records dependency versions, provenance, license, platforms, requirements and build/runtime ownership.
The generated inventory and release manifests are authoritative for exact tool pins and platform checksums; do not manually edit them.
Git and mise are host prerequisites.

## Terminal dependency

The active terminal runtime uses unmodified upstream HC 0.1.14, protocol generation one, through one typed Go client.
The staged release contains its matched HC manual and namespaced template, not a custom Sum MCP server or scoped bridge.
The selected source release is commit `29e6d375623de40cb4820e0894aa131ba057b796`.
The [versioned upstream manifest](https://releases.herdr.dev/core/0.1.14/manifest.json) records distributed artifacts.
The inspected macOS aarch64 archive SHA256 is `ca524329d2e0630219550055dcc71f42aafd85fba356e0d543d6f51244487970`.
The binary's discovery schema matches the release source after JSON normalization.
No upstream patch or fork is required.

Codex 0.166.0 with its default keymap and `--no-daemon` is the qualified automatic input path.
The effective command, keymap and configuration are hashed when the namespaced template is qualified.
Sum's template sets Tab submission; idle is guarded with `if:idle`, while observed Done uses an ordinary send without `if` or `force`.
A raced working turn queues input for the next Codex turn rather than establishing an atomic Done guard.
Status, `started` and turn counters remain diagnostic, not application receipts.
Other harnesses need explicit actual qualification; CLI flag support alone is not delivery qualification.
See [HC runtime](herdr-backend.md) and [acceptance](ACCEPTANCE.md).

## Setup and release artifacts

Setup stages local tools, the cgo-free `go/cmd/sumctl` binary, exact HC artifacts, matched manual and owned templates.
It generates no custom Sum MCP configuration on a fresh installation.
Unrelated MCP integrations and global harness/account settings remain untouched.
Only explicitly owned legacy Sum configuration may be retired after a controlled cutover; no unproven existing-state activation is implied by setup.
`release stage --ref SHA` builds an immutable checksummed bundle without selecting it.
`bin/sumctl` is the stable installation callback entrypoint used by sealed task briefs.
A release manifest identifies source, bundled file hashes, dependency/native hashes and actual runtime capabilities.
Staging is not approval or production activation.

## Go and helper tools

The Go module uses Cobra for commands and the existing TOON encoder for compact structured output.
There is no custom MCP transport dependency in the terminal runtime.
The bounded helper runner owns ordinary Git, GitHub, process-inspection and tool invocations; native HC I/O uses the typed protocol client.
Deadlines, output limits and unknown mutation effects must be preserved.
Project verification and interactive model lifecycles retain their documented owners.
LSP tools remain allowlisted and local; `bin/lsp-ensure` fails open with empty stdout when no staged helper is available.
No unknown binary, global package, model or account is installed to repair a task failure.

## Skills and optional integrations

Canonical Sum procedures live under `skills/`; tracked projections point to those sources.
Portable verification/evidence skills remain usable without Sum or HC.
Vercel Skills installation uses explicit selected skills and project copy mode, not automatic execution or global configuration mutation.
Optional codegraph is an on-demand checkout-local exploration aid, never verification or role authority.
An MCP snippet printed for a person to merge for codegraph is unrelated to the retired Sum terminal transport.
The separate Grok Bot product and its setup remain outside this refactor.

## Compatibility and generations

The stable state root selects a separate physical generation home.
Normal Open/init/status never migrate schema-one history.
Sealed policy/procedure/evidence bytes remain unchanged, and old records are not assigned fabricated HC identity.
Existing-installation cutover requires an enforceable exclusion lease covering every legacy writer and sender.
Current legacy APIs cannot establish it; process listings or operator assertions do not substitute.
Cutover must refuse without this external prerequisite.
Rollback checks the actual target binary's active schema/endpoint/receipt capabilities; an old schema-one runtime cannot reactivate an inactive archive.
See [update](update.md) and [recovery](recovery.md).
