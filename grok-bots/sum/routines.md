# Suggested routines

Do not enable a routine until two successful manual Status checks look right.
After two successful manual Status checks, the user may enable the weekday Inbox status.
This file does not enable it.
A routine performs real work.
Keep write actions behind approval.

## Inbox status

- Owner: the coordinator Bot
- Cadence: weekdays at 09:00 in the Bot time zone
- Skill: Status
- Expected result: the ops lines below, or no message when every line is empty
- Approval boundary: do not answer questions, dispatch work, merge, send, publish, or message anyone except this conversation
- Missing source: if `/workspace/sum/inbox.md` is missing, report that and stop
- Test first: yes
- Enabling: after two successful manual Status checks look right, the user may enable this routine. Do not enable it from the repository alone.

Ops lines, in order, omitting empty ones:

1. Unanswered questions, with task id and key.
2. Reports that still need a distinct verification run. Reading worker logs is not that run.
3. Failed verifications and repairs still owed.
4. Pending approvals: merge, send, publish, delete, spend.
5. Factory Review count, only when a confirmed settings file under `/workspace/sum/factories/` lists a Review lane and a piece is sitting there. A count, not the piece bodies.
6. Up to three priorities already written in the inbox or task files. Do not invent one.
7. Digests: one line in this chat plus the saved link. Do not paste the digest body.

An empty inbox, with none of those lines, stays quiet.

## Standing rule

A scheduled wake with an empty inbox may stay quiet.
A tasked ask from dispatch still needs a reply against the task id, including "nothing happened".
