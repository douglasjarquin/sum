# Native HC prerequisites

Use the current supported Herdr app with its own HC-backed terminal and matching core.
Start the app independently, enter the Sum instruction checkout, and launch your usual interactive coding agent in that terminal.
For Codex the inspected HC manual requires `codex --no-daemon` so command execution remains inside HC.
Do not switch the user's model, account, or permissions to get a demo running.

Before delegation read installed `hc --skill`, `hc run --help`, `hc send --help`, and `hc wait --help`.
Require a nonempty actual `HC_TERMINAL_ID`, then inspect `hc get "$HC_TERMINAL_ID"` to confirm the current agent/status and native lead relationship against the matching core.
Check native status integration and notifications: HC's `notes = false` disables automatic notes.
Successful RPC connection alone is insufficient; upstream HC may auto-start its own core without making this conversation addressable.
Sum adds no core launcher, supervisor, hidden lead, or bridge.

An old Herdr TUI pane such as `wJ5:p5` is not an HC terminal address such as `t10`.
A foreign PTY cannot be imported by inventing HC environment variables or mapping a remembered coordinator.
If the supported native lead or status integration is missing, report that prerequisite and use the supported launch path.
Do not restart the user's shared core or type through trust/authentication/permission dialogs.

## Inspected combination and limits

On 2026-11-03 the development host provided HC 0.1.17, Herdr app 0.1.12, Codex CLI 0.166.0, and Claude Code 2.1.318.
The exact observed artifacts and checks are in [the issue evidence](verification/issue-348.md).
The installed HC manual describes native parent defaults, notifications, explicit sends, waits, and Codex's flag.
These are CLI/manual observations, not authenticated qualification.
No app/HC/harness combination has been demonstrated end to end in this change's API conversation, which has no HC terminal identity.
Claude lead plus interactive Codex reviewer is a candidate mixed-harness qualification path, not a claim of tested support.
Other harnesses, including a terminal-launched `grok`, are unqualified here; the Grok Bot product is separate.

The issue's inspected upstream HC commit is [ec8adcdf](https://github.com/herdrdev/herdr-core/tree/ec8adcdf0da38e539e8702f7c37801647da035f3) and app commit [816d2057](https://github.com/herdrdev/herdr-app/tree/816d2057d83f0cbd3705926c0231fb12963ae0fb).
Their package version strings do not prove the inspected source behavior exists in a released binary.
Qualify the actual installed artifacts and harness launch path; invent no minimum harness versions from an animation.

## Qualification

Run the full [acceptance matrix](ACCEPTANCE.md) from an authenticated native lead.
Record actual versions/artifacts, commands, returned native addresses, stable candidate identity, incoming results, reports, and limitations.
Use upstream isolated-session guidance with a distinct HC_SESSION and scratch XDG paths for destructive lifecycle cases.
Never use the user's default session or restart their core to manufacture recovery evidence.
