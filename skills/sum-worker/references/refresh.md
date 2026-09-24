# Worker procedure: brief revisions and refresh

Part of the sum worker procedure, pinned with your brief. Read it when a `sum refresh` message or a `brief revision rN is requested` notice reaches you, or before you adopt any revision. It adds detail to the required core (`sum-worker`) and never replaces it.

Your brief is one numbered revision generated from the task record. The coordinator may stage a newer revision (updated procedure or newly recorded decisions) without touching the file you read.
`sumctl brief list TASK_ID` (the `show` command also carries a `versions` field) shows revisions, their integrity, and whether one is `requested`.
Adopt a requested revision only when the coordinator asks: read it, run `sumctl brief adopt TASK_ID rN`, and continue from your current progress. The approved task never changes between revisions; a new revision is not a new task and does not restart your implementation.

### Refresh procedure

A refresh arrives as a short fixed message that starts with `sum refresh TASK_ID: brief revision rN is requested`, names the revision file, the change summary, and the exact `brief adopt` command. When that message could not be delivered, the same request rides a later `sum returns for the worker` notice as `brief revision rN is requested` with the same `brief adopt` command; `sumctl brief list TASK_ID` shows the revision file. Treat either as a request to reread instructions, never as a new task or as authorization.
Handle it at your next safe point: after the current tool call or turn finishes, not in the middle of an edit, a test run, or a commit.

1. Finish or cleanly pause the step in progress. Do not abandon partial edits or interrupt an in-flight command.
2. When the message says only recorded decisions changed, read them with the `context ... --section decisions` command it names; your procedure and the rest of your brief are unchanged. Otherwise run `sumctl brief list TASK_ID` and read the requested revision file completely: its `## Brief revision` section carries the machine-generated change summary, `## Recorded decisions` lists the decisions to compare with what you applied, and `## Worker procedure` names the pinned procedure files you now follow; read every required one whose sha256 you have not read before, and an on-demand one when its condition applies.
3. Run `sumctl brief adopt TASK_ID rN`. That records a receipt: evidence that you read the revision, nothing more.
4. Continue from your saved progress in the same checkout and session: keep completed implementation, existing commits, the report you already submitted, and your repair count. Do not redo finished work, republish a PR, reset repair accounting, change harness, model, or account, or restart yourself.
5. Apply newly answered decisions with `sumctl resolve` as usual.

If `brief adopt` refuses because a newer revision was requested meanwhile, read and adopt that one instead; an older receipt never activates a superseded revision.
If no refresh message reaches you, nothing changes: the brief you have stays valid, and the coordinator sees the refresh as pending.

