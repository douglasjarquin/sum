# Delivery pipeline

Every task that ends in a GitHub PR passes nine delivery gates: Intent, Rebase, Review, Test, Document, Lint, Push, PR, CI.
The order and the display names are one table in `go/internal/pipeline/pipeline.go`; a record always carries one row per gate.
Rows are derived from the task's own records, never set by hand, so the table cannot drift from the evidence it summarises.
The record is a pipeline.json file beside the task.json under the task record.
The implementation is `go/internal/pipeline`, registered as commands in `go/internal/cli/pipeline.go`; `go/internal/prcmd/pr.go`, `go/internal/verifycmd/verify.go`, `go/internal/review/review.go`, and `go/internal/report/report.go` re-derive the record after they change the facts it reads.

This release runs five of the gates. Intent, Review, Test, Document, and PR read facts sum already holds. Rebase, Lint, Push, and CI render as pending and run nothing.

Each project keeps owning its own `VERIFY.md` and `mise run verify`, which work in any clone without sum. The pipeline gates sit around that contract and never edit it.

The table is the coordinator's record of what has run. It is not verification, review, or a merge decision, and only the user merges.

| ID | Scenario | Driver | Evidence |
| --- | --- | --- | --- |
| `pipeline.derive` | Every row is computed from the task record alone: an approved brief passes Intent, a coordinator verification run for the current candidate decides Test, review verdicts decide Review, a reconciled PR decides PR, and the gates this release does not run stay pending | automated: `go/internal/pipeline/derive_test.go` | offline suite |
| `pipeline.derive-caveats` | A dirty or inconclusive run blocks Test and carries its blocked reason; a run that certifies no candidate or requires root review says so in the row; a PR finding blocks the PR row instead of passing it | automated: `go/internal/pipeline/derive_test.go` | offline suite |
| `pipeline.record` | A task with no recorded pipeline reads as an all-pending record, so no caller branches on existence; writing the same row twice writes nothing | automated: `go/internal/pipeline/record_test.go` | offline suite |
| `pipeline.refresh` | `verify` and `review` re-derive the record after they save, and `pipeline show` prints every gate | automated: `go/internal/cli/pipeline_test.go` | offline suite |
| `pipeline.block` | The published block carries the stage table, the approving reviewer's summary, and one collapsible `#### Pass N` per candidate that was sent back; the section is omitted when nobody has reviewed; a marker quoted in review text cannot split either block | automated: `go/internal/pipeline/block_test.go` | offline suite |
| `pipeline.block-ownership` | Exactly one marked span is owned: a malformed, inverted, or duplicated pair is refused, and a splice keeps every byte outside the span | automated: `go/internal/pipeline/block_test.go` | offline suite |
| `pipeline.publish` | `sumctl pipeline publish` writes one `<!-- sum-pipeline:* -->` block beside the evidence block, preserves the human prose, and records one `publication` record with `block: pipeline`; a second publish is `unchanged` and edits nothing; `--dry-run` edits nothing | automated: `go/internal/cli/pipeline_test.go` | offline suite |
| `pipeline.publish-foreign` | A block edited by hand is refused with the body left intact until `--replace-foreign-block` takes it over, and the takeover still keeps the prose outside the block | automated: `go/internal/cli/pipeline_test.go` | offline suite |
| `pipeline.publish-auto` | `pr reconcile` writes both the evidence block and the pipeline block into an open PR and reports `pipeline_publication`; `"evidence": {"auto_publish": false}` in `.sum/settings.json` turns both off | automated: `go/internal/cli/pipeline_test.go` | offline suite |
| `pipeline.document` | `sumctl pipeline document` audits the candidate's own `VERIFY.md` and feature maps in a throwaway detached checkout of that SHA, records one `documentation` record with the audit id and finding counts, and removes the checkout; a project with no `VERIFY.md` is `skipped`, never failed | automated: `go/internal/cli/pipeline_test.go` | offline suite |
| `pipeline.publish-unreconciled` | Publishing before `pr reconcile` fails naming that command and edits nothing | automated: `go/internal/cli/pipeline_test.go` | offline suite |
| `pipeline.pr-body` | A real reviewer reads the published table in a real PR and finds it matches what sum recorded | manual: publish a sum task's own PR and compare the block with `sumctl pipeline show TASK_ID` | the PR URL in the task record |
