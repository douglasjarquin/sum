# Independent adoption and updates

Use a separate checkout/path for the instruction distribution.
Do not cut over a serving legacy installation through its self-updater, swap its runtime links, transfer terminal ownership, or import its state.

1. Preserve the existing legacy checkout at its existing commit, including installed helper artifacts, task records, worktrees, branches, and callback paths.
2. Let its tasks finish or explicitly settle them through their existing trusted helper/procedures.
3. Clone the new instruction distribution into a different path; start new tasks there from an existing supported HC-backed Herdr terminal.
4. Carry useful preferences and unresolved decisions forward as ordinary data, never as native authority inferred from old records.

Update the instruction checkout using ordinary Git between sessions after inspecting and preserving local work.
Keep the procedure checkout available while any helper brief references its absolute paths.
Do not change instructions underneath active helpers; finish or settle them first.

If the new workflow cannot be used, manually return to the preserved legacy checkout and its complete compatible procedures for new legacy work.
This is no automatic rollback, importer, or permanent dual-runtime mode.
Merging a PR does not authorize updating a running installation, changing accounts, stopping a shared core, or restarting sessions.
The separate [Bot software-factory lane](legacy.md) remains on its compatible legacy target.
