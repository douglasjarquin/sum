# Repairs

Controlled repair sends and the expansion allowance. Moved out of the README.

### Controlled repairs

Each task starts with an allowance of two SUM-controlled repair iterations, spent only on out-of-scope corrections.
Send a correction to its settled worker with the current attempt ID and a stable instruction key:

```sh
./bin/sumctl repair send TASK_ID --attempt ATTEMPT_ID --key correction-1 --file /absolute/path/to/correction.md
```

Every send carries a class.
The default `in-scope` covers whatever the worker needs to satisfy its approved brief - a rebase behind a moving main, CI, lint, documentation, or verification failures, push divergence, conflict resolution after upstream merges, malformed-report re-reports - and consumes nothing.
A send for work outside the approved brief is an `expansion` and needs a recorded reason; only those sends consume the allowance:

```sh
./bin/sumctl repair send TASK_ID --attempt ATTEMPT_ID --key correction-2 --class expansion --reason "the extra endpoint the user asked for" --file /absolute/path/to/correction.md
```

The command records the instruction before delivery and refuses a busy worker, a changed attempt, or an attempt that is not `running` (an `uncertain` launch included).
Repeating the same key and instruction reads the saved outcome without sending or charging again.
A delivery Herdr refuses before it reaches the worker records nothing and charges nothing.
An uncertain expansion delivery stays charged, including when the helper exits after saving its intent; a queued or mid-turn delivery counts as delivered.
Use a new key only for an explicitly requested new iteration.

`execution resume` relaunches already-approved work and consumes nothing.
Initial dispatch, observations, notifications, required worker and coordinator verification, and brief refresh do not consume extra iterations.
Candidate, harness, and runtime changes do not reset the task record.
Read the `repairs` field in `sumctl show TASK_ID` for classified operations, the consumed count, and grants.

Expansion exhaustion refuses another expansion send - in-scope sends are never blocked - and saves one budget decision in the task's questions.
It stops no worker, frees no slot, and preserves other obligations.
Only after the user approves an additional allowance, record that decision from the coordinator pane:

```sh
./bin/sumctl repair extend TASK_ID --question QUESTION_ID --additional 1 --approved --file /absolute/path/to/user-decision.md
```

The grant is tied to that exhaustion decision; repeating an identical grant adds nothing.
An ordinary answer or worker report does not grant more iterations.
If the decision is already answered, explicit confirmation must preserve its exact text.
Worker-internal loops and commands issued directly to an external harness remain outside this mechanism.
Older helpers can preserve the records without enforcing this policy.
