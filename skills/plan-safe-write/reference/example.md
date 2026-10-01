# Worked example: tagging devices

Illustrative: the shape of a good run, with results shortened.

**Request:** "Tag r1 and r2 as edge."

1. Read first: `inspect-inventory` shows `r1` and `r2` exist; the network has a tag `edge` carried by `r3`.
2. Plan: `edit-device-tags` `{action: "add", devices: ["r1","r2"], tags: ["edge"]}` (no `apply`) -> `ok`, `mode: dry_run`, one change: add tag `edge` on `r1, r2`; `r3` already has it and is not touched. `undo`: run with action `remove` on `r1, r2`. Limit: takes effect in the next snapshot.
3. Show it: "I would put `edge` on r1 and r2 (r3 already has it). It takes effect in the next collection and I can undo it by removing it from those two. Shall I apply?"
4. **Wait.** The person says yes.
5. Apply once: the same call with `apply: true` -> `ok`, `mode: applied`; the skill read the tags back and both devices carry `edge`.
6. Report: done, with the undo.

If the person had said "tag them with a new tag called dmz", the skill would refuse (it never creates a tag; a tag definition cannot be removed through the API): say so and ask them to create the tag in Forward first.
