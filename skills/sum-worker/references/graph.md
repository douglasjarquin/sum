# Worker procedure: code graph

Part of the sum worker procedure, pinned with your brief. Read it when your brief's `## Code graph` section, or `context --section execution`, reports an index other than `not built`, or before you ask for one. It adds detail to the required core (`sum-worker`) and never replaces it.

Your brief's `## Code graph` section says whether your checkout has a codegraph index. It is usually `not built`: sum indexes a checkout only when the coordinator asks. Otherwise it is `ready` with the exact CLI commands, or why not (`failed`, `exhausted`, `unavailable`).
A `ready` index is `.codegraph/` inside your checkout, built by the pinned codegraph of the runtime, in CLI mode: nothing watches it, so run the brief's `sync` command after you edit or commit and before you query; `status --json` reports only uncommitted edits as pending, and a commit, checkout, or rebase leaves the index silently behind until you sync.
Use `explore`, `query`, `node`, and `affected` as exploration aids only. A result that contradicts a file, a pending sync, or any state other than `ready` means read the source; never turn a graph result into a structural conclusion, a verification result, or a reason to skip a mapped check.
Do not run `codegraph init`, `index`, `install`, `upgrade`, `serve`, or `uninstall` yourself, do not point a query at the primary clone or another worktree, and do not edit MCP or harness configuration; the coordinator owns index initialization (`sumctl graph init TASK_ID`) and prints any MCP snippet for a person to merge. Ask through `sumctl ask` if an index would materially help. `context --section execution` carries the current `graph` state if it changed after your brief was written.

