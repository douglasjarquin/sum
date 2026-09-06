---
name: evidence
description: Capture truthful before/after evidence for one mapped scenario - real screenshots, short screencasts, CLI transcripts, or HTTP responses from the base and the candidate builds - and compare them into one manifest. Works in any clone without sum or Herdr.
---
# Evidence

This skill is portable: everything it needs lives inside the repository you are verifying, plus Python 3.11+, and for browser captures Node 22+ and a Chromium-family browser already on the machine.
It does not require sum, Herdr, a `.sum` directory, or any absolute path outside this checkout.
It never starts, stops, or resets an application and never drives a person's browser profile; you run the base and the candidate yourself, in their own checkouts, and point each capture at the one you drove.

Evidence complements `.agents/skills/verify/`: a green `mise run verify` says the automated checks passed, this skill shows a person the user path failing before and passing after.
Images alone prove nothing; every capture also records what the page, command, or response said, and the comparison refuses to call two captures a red/green pair unless the assertions say so.

## Procedure

1. Run `python3 .agents/skills/evidence/scripts/evidence_capture.py capabilities` first. `browser: supported` needs Node 22+ and a browser binary (`EVIDENCE_BROWSER=/path` overrides discovery). A recipe this machine cannot drive is recorded `blocked`, never simulated; report such scenarios with the runner's `--scenario` instead.
2. Pick the scenario id from the feature maps (`VERIFY.md` -> `feature_maps`) and its kind: `bugfix` (a defect the user could see), `feature` (nothing to fail before), `visual` (an intended change of appearance), or `nonvisual` (CLI/API/library; visual proof is not applicable and no browser screenshot is manufactured).
3. Identify the two revisions: the base (merge base of the branch) and the final candidate SHA. Check out each into its own directory (`git worktree add ../base <sha>`), build and start each with the repository's own commands on distinct ports. Never reuse a checkout someone else is editing.
4. Capture the before state from the base checkout, then the after state from the candidate, with one shared run id and identical settings (viewport, theme, locale, timezone, scenario data, flags):

   ```sh
   RUN=$(date -u +%Y%m%dT%H%M%SZ)-$RANDOM
   python3 .agents/skills/evidence/scripts/evidence_capture.py capture --scenario <id> --role before --kind bugfix --run $RUN --checkout ../base --expect-sha <base-sha> \
       browser --url http://127.0.0.1:<base-port>/ --step click='#add' --step wait=500 --observe '#count' --expect-selector '#count=Count: 1'
   python3 .agents/skills/evidence/scripts/evidence_capture.py capture --scenario <id> --role after --kind bugfix --run $RUN --checkout ../candidate --expect-sha <candidate-sha> \
       browser --url http://127.0.0.1:<candidate-port>/ --step click='#add' --step wait-text='Count: 1' --observe '#count' --expect-selector '#count=Count: 1'
   python3 .agents/skills/evidence/scripts/evidence_capture.py compare --scenario <id> --run $RUN --base <base-sha> --candidate <candidate-sha>
   ```

   The browser recipe dispatches real mouse and keyboard events (`click=`, `type=SEL=TEXT`, `press=Enter`), waits on visible text (`wait-text=`), records a screenshot and a screencast of the whole path, and reads the visible text back as the observable end state. Add `--side-effect GET <url> --side-effect-text '...'` when the journey must also change server state. Add `--convert` for an mp4 beside the AVI original when ffmpeg is present.
   For a CLI or API change use `cli -- <command>` (transcript with exit code) or `http METHOD URL` (status and body) with `--kind nonvisual`; the manifest says `visual_proof: not-applicable`.
   When the base cannot be run (no access, no build), record it honestly instead of inventing a red screenshot: `evidence_capture.py unavailable --scenario <id> --role before --reason '...'`; the comparison is then `after-only` with the label `before-unavailable`.
   Do not break code on purpose to produce red. A bug fix that already passes at the base is reported `before-also-passes`.
5. Read the comparison. `red-green` means the base failed the assertions and the candidate passed them with matching environments and builds. `mismatch` names a viewport, theme, locale, recipe, or kind difference you did not declare with `--intentional key=value`, a capture taken from the wrong SHA (stale build), or an original whose hash changed. `capture-failed` keeps the diagnostics (`browser-diagnostics.log`, driver exit, console) and is never a pass.
6. Report the paths: `<evidence>/<run>/<scenario>/comparison.json`, the `before-<sha>/` and `after-<sha>/` directories, and `comparison.html` (a derived two-up view that references the originals and only scales them with CSS). When the checkout is disposable, run `evidence_capture.py promote --run $RUN --to <durable-dir>` before cleanup; it copies the run and re-verifies every recorded content hash, or set `VERIFY_EVIDENCE_ROOT` to a durable directory before capturing.

## Publish into the pull request

`scripts/evidence_publish.py` places the captured media into one pull request's description as a single marked block (`<!-- before-and-after:start -->` ... `<!-- before-and-after:end -->`).
Capture, publication, and the block format stay separate: this script never drives a browser or converts video, and the only upload path is the GitHub CLI's own `gh pr edit --attach` (GitHub CLI 2.99+), running as whoever invokes it with whatever authorization they already hold.
Publication is a deliberate, authorized operation against your own repository; capture never needs it, and a worker or verification script is never handed write credentials to perform it.

1. `python3 .agents/skills/evidence/scripts/evidence_publish.py capabilities` reads the installed `gh`: its version, whether `pr edit --attach` exists, and whether it is authenticated. Without `--attach`, capture still works and publication is explicitly deferred; nothing is downloaded or installed per publication.
2. `plan --run $RUN --repo owner/name --pr N --candidate <sha> [--base <sha>] [--scenario <id>]...` validates every comparison in the run and stages approved publish copies under `<run>/publish/media/` (or `--publish-dir`), uploading nothing.
   A scenario is planned only when its comparison names the same candidate, its verdict shows the candidate passing (`red-green`, `before-after`, `after-only`, `before-also-passes`; never `mismatch`, `capture-failed`, `after-fails`), every media file resolves inside the run directory without symlinks or parent components, its bytes match the comparison and capture hashes and re-read dimensions, it is within GitHub's limits (10 MiB images, 100 MiB videos), and no secret-shaped text (the capture redaction patterns plus `--redact`) appears in anything that would reach the PR. A capture whose text was redacted is refused with its screenshot, because a screenshot cannot be redacted. Only PNG/JPEG/GIF/WebP and MP4/MOV/WebM are attachable; an AVI-only screencast is named as unsupported and the screenshot pair is published without it (capture with `--convert` to get an mp4).
3. `publish --plan <plan.json> --receipts <path> --visibility public|private` reads the destination (`gh repo view`: exact name, visibility, write permission) and the latest PR body, refuses a PR that is not open or whose head is not the candidate (`--allow-head-mismatch` publishes it labelled), then edits only the marked block: append when none exists, replace in place when the last block came from these receipts, refuse when the block was written or edited by someone else (`--replace-foreign-block` takes it over after you inspected it). Prose outside the block is copied byte for byte from the body read immediately before the edit; the body is read again before writing and the edit is recomputed when it moved.
   Receipts persist one entry per content hash and destination: a second publication reuses the attachment URLs, uploads nothing, and replaces the same block. Videos go up on their own lines (GitHub renders a player) and become a side-by-side HTML `<video>` table in a second edit once every URL is known. After every edit the body is read back and checked: no local references left, a URL for each attached hash, unchanged prose; otherwise the previous body is restored and the outcome is `failed`, with whatever did upload kept in receipts for reuse.
   A timeout after a possible write is `uncertain` until the PR is read back: attachments found there are recorded; an unchanged body means nothing was applied. Exactly-once attachment creation is not promised where the API cannot establish it; receipts and `results/*.json` under the publish directory record what happened.
4. Keep the receipts and publish directory where they outlive a disposable checkout (`--receipts`, `--publish-dir`), as sum does under the task record; the originals under the evidence root are never modified or removed by publication.

The block carries the scenario and feature id, verdict, candidate and base SHAs, the evidence run, cited verification run ids, and who captured it, and says in words that worker media is a claim about the candidate build and not the coordinator's root verification.
Block markup follows `vercel-labs/before-and-after` (see `references/ATTRIBUTION.md` and `references/LICENSE-before-and-after.txt`); its markers are the same, so a block written by that skill is recognised as foreign and left alone unless you take it over explicitly.

## What a capture records

`capture.json` beside the media: scenario and feature id, role and kind, the recipe and its exact steps, the checkout path, HEAD SHA, dirty state and branch, the expected SHA and whether it matched, an optional build id, start/end times, environment (viewport, theme, locale, timezone, browser version, platform, Node and Python versions; environment variable names for CLI, never values), every media file with type, bytes, sha256, and real dimensions, frame count and duration read from the bytes, the assertions with met/unmet, observations (redacted), console messages, limitations, intentional differences, and the redaction count.
Screencasts are the original JPEG frames under `frames/` plus a Motion-JPEG AVI assembled from them at a fixed frame rate; held repeats are timing only and are counted in the manifest. A converted mp4 is marked `derived` with the tool and command.
Video is bounded: at most 60 seconds, 1920x1080, 25 MB; the manifest says when a bound stopped the recording.
Recorded text is redacted for bearer tokens, `key=`/`token=`/`password=` shapes, emails, and common API key prefixes (`--redact REGEX` adds patterns); a redacted transcript is named `*.redacted.txt`. Screenshots cannot be redacted, so use synthetic data. The browser always runs with a fresh temporary profile, so no cookies, sessions, or extensions of the person leak in. `inspect PATH` validates any media file by reading it.

## Rules

- A capture is never overwritten, edited, or regenerated to look better; a second attempt is a new run id with the earlier one kept.
- Observations are read-only (visible text, title, URL, one selector's text); never manipulate the DOM or internal state to reach the end state.
- The evidence root comes from `evidence` in `VERIFY.md` (default `.artifacts/evidence`, Git-ignored) or `VERIFY_EVIDENCE_ROOT`; run ids keep concurrent captures apart, and each role directory carries the SHA it was taken from.
- A comparison is evidence for a person or a root session to read; it does not certify a candidate, replace the verify runner, or bypass the independent review.
- Publication changes nothing about that: a block in a PR is a copy of the worker's claim for reviewers to see, never verification, approval, or a merge.
