# Contributing to sum

Use an isolated development checkout and keep the installation serving live work untouched.

## Checklist

- [ ] Confirm the repository root, target branch, and the task's approved scope.
- [ ] Install only the pinned verification prerequisites with `mise install go python node`; Git must also be available.
- [ ] Make the smallest change that satisfies the approved acceptance criteria.
- [ ] Run `MISE_ENABLE_TOOLS=go,python,node python3 .agents/skills/verify/scripts/verify_run.py --base <merge-base>` once for your role and retain its candidate-bound record.
- [ ] Run `mise run test-live` separately only when a real Herdr smoke test applies and is available; otherwise leave that manual scenario accurately not-run.
- [ ] Describe acceptance results and evidence paths in the handoff, including failures or manual scenarios not run.
- [ ] Include contributor attribution when applicable and state deployment or release impact when relevant.
- [ ] Leave branch-protection and merge actions to their owner; a human reviews and merges changes.

The recommended order is scope and prerequisites, implementation, canonical verification, applicable live verification, review, then human merge.
Only explicit technical dependencies are prerequisites; a roadmap's recommended order does not create a dependency.
The worker's run, a fresh root run for the same candidate, and independent review are all required before human merge.
CI supplies additional evidence; an absent CI result, worker-only pass, or missing root review is not merge readiness.
