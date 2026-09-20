# Suggested routines

Do not enable a routine until two successful manual Rundowns look right.
After two successful manual Rundowns, enable the weekday Inbox rundown.
A routine performs real work.
Keep write actions behind approval.

## Inbox rundown

- Owner: the coordinator Bot
- Cadence: weekdays at 09:00 in the Bot time zone
- Skill: Rundown
- Expected result: a short list of unanswered questions, unverified reports, and failures, or no message when the inbox is empty
- Approval boundary: do not answer questions, dispatch work, merge, or message anyone except this conversation
- Missing source: if `/workspace/sum/inbox.md` is missing, report that and stop
- Test first: yes

## Standing rule

A scheduled wake with an empty inbox may stay quiet.
A tasked ask from dispatch still needs a reply against the task id, including "nothing happened".
