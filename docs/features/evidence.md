# Before/after evidence

The `.agents/skills/evidence/` skill: capture the same mapped scenario from a checkout of the base SHA and from the candidate, as real screenshots, screencasts, CLI transcripts, or HTTP responses, and compare the two into one manifest.
Browser scenarios are driven through the DevTools Protocol from `.agents/skills/evidence/scripts/evidence_browser.mjs` (Node 22+, Chromium-family binary); the suite skips them with a visible reason when no browser is installed and proves the blocked path instead.
Seeded fixtures: `tests/fixtures/evidence/counter/index.html` (a counter whose display lags the click by one) and `tests/fixtures/evidence/greet/greet.py` (a CLI that exits 1 on success and prints a credential-shaped token); each test commits the fixture as the base and its fix as the candidate.

| ID | Scenario | Driver | Evidence |
| --- | --- | --- | --- |
| `evidence.capabilities` | The skill reports which recipes this machine can drive and never claims a browser it cannot find | automated: `tests/test_evidence_skill.py` | offline suite |
| `evidence.browser-red-green` | A seeded counter bug reproduced with a real click at the base and fixed at the candidate: screenshot pair, playable nonempty screencast with real frame count and dimensions, `red-green` verdict | automated: `tests/test_evidence_skill.py` (skipped with reason without a Chromium-family browser) | offline suite (media validated by reading bytes in a temporary repository) |
| `evidence.cli-red-green` | A CLI defect fails at the base and passes at the candidate through transcripts; visual proof is marked not applicable | automated: `tests/test_evidence_skill.py` | offline suite |
| `evidence.mismatch-detection` | A capture from the wrong SHA is blocked as a stale build; before/after viewport or theme differences without `--intentional` make the comparison `mismatch`; an altered original fails its hash | automated: `tests/test_evidence_skill.py` | offline suite |
| `evidence.before-unavailable` | A missing baseline is recorded `unavailable` and the comparison is `after-only` labelled `before-unavailable`, never an invented red | automated: `tests/test_evidence_skill.py` | offline suite |
| `evidence.after-only-feature` | A new feature is compared honestly as `before-after`/`after-only` and a bug fix that already passes at the base is `before-also-passes` | automated: `tests/test_evidence_skill.py` | offline suite |
| `evidence.recorder-failure` | A missing or crashing browser leaves a `blocked` capture with diagnostics and a `capture-failed` comparison, never a pass | automated: `tests/test_evidence_skill.py` | offline suite |
| `evidence.redaction` | Credential-shaped text and emails in transcripts are redacted, the copy is labelled with a redacted suffix, and environment values are never recorded | automated: `tests/test_evidence_skill.py` | offline suite |
| `evidence.concurrent-captures` | Worker and root captures of the same scenario run at the same time into separate run ids without interfering | automated: `tests/test_evidence_skill.py` | offline suite |
| `evidence.promote-preserves-originals` | A run promoted out of a disposable checkout keeps every original byte for byte (hashes re-verified) after the checkout is removed | automated: `tests/test_evidence_skill.py` | offline suite |
| `evidence.runner-reports-missing` | A map row that names visual evidence is reported by `.agents/skills/verify/scripts/verify_run.py` as missing required evidence until a comparison for the candidate exists | automated: `tests/test_evidence_skill.py` | offline suite |
| `evidence.external-clone` | The skill runs from a plain clone with sum absent: capabilities, CLI captures, comparison, inspect, promote | automated: `tests/test_evidence_skill.py` | offline suite |
| `evidence.sum-self` | A sum candidate that changes something a user sees ships a comparison manifest under `.artifacts/evidence/` | manual: follow `.agents/skills/evidence/SKILL.md` in a task checkout | comparison manifest path in the task report |
