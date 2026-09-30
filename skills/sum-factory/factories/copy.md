# Copy factory

runner: directive
floor: notion
floor-ref: https://app.notion.com/p/3e46f7519df381e990b5ee7a7d1b61fe

Pieces: https://app.notion.com/p/67bbdc921045444983614789743dda3b
Pieces data source: `collection://fefba075-ec0f-4b93-b915-5ef21db2cb18`

This file is the recommendation for the floor that already exists. Confirm it with the user before the first run. When they correct a lane or an action, save their words to `/workspace/sum/factories/copy.md` and follow that file next time.

Spiral writes. The user approves. Nothing here posts.

## Lanes

Inbox
Brief
Ready
Drafting
Review
Revise
Approved
Parked

Brief can be skipped. Parked is off the line. The user sets Approved.

On a start, fetch the Pieces Status options and say the names you saw. If they differ from this list, ask which order to keep.

## Pickup

- Ready
- Revise, only when Revision notes are non-empty

Review is the user's turn. A pass does not pick up Review.

A piece URL is the reliable trigger. If no URL was given, search the Pieces data source for Ready, then Revise. Fetch each hit. Keep it only when Status is exactly Ready, or exactly Revise with revision notes. If none confirm, ask for a link. Do not draft from a title match.

## Actions

### Ready

### Revise

Both lanes use the same write-back. Revise sends the revision notes. Ready sends the Assignment.

Resolve the piece, then its Product, then that product's Workspace row, then its Channel.

- The workspace id is the workspace row's `Spiral workspace id`.
- `--style` is the channel's `Spiral style id`.
- `--prompt` is the channel's `Saved prompt`.

Omit a flag when the value is empty. Do not paste voice or format notes into the prompt. Those live in Spiral.

Many products may share one workspace row. niceuptime and nicebaas share the niceapps row. Do not copy the only workspace id onto a blank row.

If the workspace id is blank, do not call Spiral. Leave the status. Append a Run log line that the workspace id is missing. Stop that piece.

Use `npx @every-env/spiral-cli@latest` and `--json`.

- Ready, with no session yet: `write "<assignment>" --workspace <id> --instant --json`
- Revise, with a session id and revision notes: `write "<revision notes>" --session <id> --json`
- Revise, with notes and no session id: `write` the draft already on the page plus the notes, with `--workspace <id> --instant --json`
- The user asked to rewrite the draft into their voice: `personalize` the draft text
- The user asked to strip AI tells and keep the voice: `humanize` the draft text

Set Status to Drafting before the call.

Read the JSON. Take the session id and `drafts[0].content`. The `text` field is a status blurb, not the draft.

The page body has `## Draft` and `## Run log`. Replace the draft section. Append one Run log line with the session id and the command name (`write`, `personalize`, or `humanize`). No tokens.

Then set Status to Review, store `Spiral session id` when `write` returned one, set `Drafted at` to today, and clear Revision notes when those notes were sent.

On failure, set Status back to the status it had before Drafting, and append the error without secrets.

Exit 2: run `login --json`, give the user the `auth_url`, and stop. A subscription or quota error: show the `upgrade_url` and do not retry.

### Spread

Only when the user asks to spread a piece. Do not call Spiral. Create Brief pieces for the product's other channels, copy the assignment, and link Same story both ways. The user sets Ready when they want those drafted.

## Never

Do not publish to a blog, X, LinkedIn, niceuptime, or nicebaas.
Do not set Approved, Inbox, Brief, Ready, or Parked.
Do not invent a Spiral workspace id.
