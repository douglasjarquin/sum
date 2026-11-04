# Review and PR evidence verification

This change adds mandatory independent review and a common PR artifact contract while extending the portable publisher to CLI/API text.

## Local evidence

The original publisher refused real CLI capture/comparison output because it contained no screenshot or converted screencast.
Nine new publication regression tests failed before the implementation changed.
The final focused publisher suite passes 30 tests, including real CLI/HTTP capture-to-publication, text-only operation without attachment capability, mixed media, escaping, redaction, baseline unavailability, changed or missing publish copies, body limits, preservation of concurrent edits during read-back, and a durable result when GitHub CLI is missing.
The package suite passes six tests, including shared template discovery through both harness paths.
These are offline fixture results, not authenticated native acceptance or hosted publication proof.

## Native acceptance

| Required scenario | Status | Evidence or limitation |
| --- | --- | --- |
| Distinct reviewer launched after builder completion | Not run | This API developer session has no HC_TERMINAL_ID |
| Seeded finding corrected and reviewed again through native messages | Not run | No authenticated HC parent/child conversation available |
| Matching PR artifacts across two harness/skill-stack combinations | Not run | Requires supported authenticated native conversations |
| Unavailable baseline, publication failure, and changed candidate reported truthfully by native agents | Not run | Offline publisher tests cover tool behavior only |

The implementing developer session can inspect source, run offline checks, and deliver an incomplete draft PR.
It cannot infer native parentage, report arrival, or cross-harness workflow compliance from those results.
Final candidate, canonical verification, independent code review, hosted CI, and actual PR publication results belong in the delivered PR's verification table.
No merge or production adoption is implied.
