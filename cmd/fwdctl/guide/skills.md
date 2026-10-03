# Running skills

    echo '<inputs as JSON>' | fwdctl run <skill>
    fwdctl run <skill> --input inputs.json
    fwdctl run <skill> --help        # what it answers, inputs table, example

## Reading a result

Every skill prints the same envelope:

| Field | Meaning |
|---|---|
| `status` | `ok`, `failed` (a finding), `unknown` (could not decide) or `error` (the skill could not run) |
| `finding` | one sentence |
| `confidence` | `deterministic` (read directly from the twin), `inferred`, or `unknown` |
| `evidence` | what the answer rests on, each item with the snapshot and operation it came from |
| `limits` | what was **not** measured, truncated or unavailable. Read this before relying on the result |
| `next_actions` | skills that logically follow |
| `context` | the network and snapshot the answer is about |

**`unknown` is never a pass.** An empty result, an unprocessed or predicted snapshot, an incomplete comparison: each is `unknown`,
with the reason in `limits`.

## Skills that write

Skills named `edit-*` change Forward's own data (a snapshot note, a check, a change set, a collection). They never touch a device.
Each is a **dry run** unless the input has `"apply": true`: the result has `mode: dry_run` and the exact `changes` it would make.
An applied result lists each change with the value it replaced and how to undo it. Some changes cannot be undone through the API
(deactivating a check, a prediction, stopping a collection): the result says so.

## Choosing a skill

`fwdctl describe plan-investigation` maps a symptom to a skill. Two longer procedures, `plan-change-review` and
`plan-incident-triage`, order several skills for "is this change safe" and "is the network healthy".

Retired skill names still work: `analyze-blast-radius` runs `verify-change` with `view: impact`, `inspect-checks` runs
`check-network-compliance` with `view: read`, and so on. `fwdctl list` shows the current names.

## Options

`--ops` adds the log of every Forward call the skill made (audit data; omitted by default to save tokens).
