# Counter fixture

A one-page counter used by `go/internal/skilltest/evidence_test.go`. The committed page carries a seeded defect (the display lags the click by one);
the test creates the base and the fixed candidate as two commits of a temporary repository, serves each from its own worktree, and drives the
real click through the evidence skill so the base fails `Count: 1` and the candidate passes it.
