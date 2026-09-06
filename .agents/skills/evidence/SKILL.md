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
