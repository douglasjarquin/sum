package skilltest

import (
	"strings"
	"testing"
)

func TestDeliveryPolicyPreservesStandingReviewAndPublicationConstraints(t *testing.T) {
	root := repoRoot(t)
	policy := strings.Join(strings.Fields(readFile(t, root+"/skills/sum-deliver/SKILL.md")), " ")
	cases := []struct {
		name    string
		clauses []string
	}{
		{"comment findings and exceptions", []string{
			"Treat any added code comment or edit to an existing code comment in the candidate diff as a blocking finding.",
			"Do not flag comments left unchanged while surrounding code changes.",
			"Commit messages, PR descriptions and `sumctl report` text are not code comments.",
			"a directive may be added only when the code cannot work without it and must include no explanatory prose.",
		}},
		{"test edits require user scope", []string{
			"Treat any added or expanded test, and any deleted, rewritten, skipped or disabled existing test, in the candidate diff as a blocking finding, unless the user explicitly asked for that test in this task.",
			"Do not flag a candidate because existing tests still run.",
		}},
		{"independent candidate review", []string{
			"The reviewer receives the task contract and candidate, not an instruction to rubber-stamp the worker's summary.",
			"Do not have simultaneous writers in the checkout.",
			"If a separate reviewer is unavailable, say independent review was not performed; do not label the result fully reviewed.",
			"Tie the review to the exact candidate SHA.",
			"Re-run required checks for later candidates; review the intervening changes rather than treating the previous SHA's approval as current.",
		}},
		{"current brief and policy", []string{
			"Check `sumctl brief list TASK_ID`:",
			"`verification_policy_changed_since`",
			"`contract_changed_since_dispatch`",
			"Review under the current policy before accepting that evidence.",
		}},
		{"bounded authorized repairs", []string{
			"Limit repair cycles; escalate repeated failure rather than opening an endless review/fix loop.",
			"Execution resumes consume no allowance.",
			"An uncertain expansion send stays charged and its key is never automatically replayed;",
			"At exhaustion, present the saved budget decision instead of launching another expansion repair.",
			"Record additional iterations only after the user's explicit approval, using `repair extend` with that question ID and the actual decision text.",
		}},
		{"review returns require recorded action", []string{
			"Every recorded review, including `--tool made`, is a `review:ID` return to you,",
			"the notice itself closes nothing and approves nothing.",
			"A newer candidate marks earlier records as not current; it deletes nothing and restarts nothing.",
		}},
		{"verification authority and waivers", []string{
			"Capacity refusal does not waive independent verification or authorize raising the limits.",
			"never treat an image without matching assertions or a comparison from another SHA as proof.",
			"ask them first, because waiving is the coordinator's recorded act on their behalf, never your own judgement.",
			"The audit runs nothing and edits nothing; a candidate that only changed the map has authored a claim, not produced evidence.",
		}},
		{"publication and cleanup authority", []string{
			"Never change accounts or credentials automatically.",
			"Push the recorded task branch without force.",
			"No exactly-once publication claim is made by this MVP.",
			"Report blockers to the user as they are.",
			"Every blocker is named; inspection removes nothing.",
			"Never merge, waive a failing required check, or tear down unfinished work.",
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, clause := range tc.clauses {
				if !strings.Contains(policy, clause) {
					t.Errorf("standing delivery policy is missing %q", clause)
				}
			}
		})
	}
}
