# Skill evaluations

Three or more cases per skill, in the shape Anthropic's skill guides ask for: queries that **should** trigger the skill
(obvious and paraphrased), queries that should **not**, and the behaviours a correct run shows. `go test ./skills` checks
that every skill has them; it does not run them, because running needs a harness with a model.

Each `evals/skills/<skill>.json` holds `cases` of `{query, should_trigger, expected_behavior, note}`.

## Running them

`cmd/skill-eval` runs the cases with the `claude` CLI, with and without the skills, and judges each run with a model:

```bash
go build -o /tmp/evalbin/fwdctl ./cmd/fwdctl && go build -o /tmp/skill-eval ./cmd/skill-eval
export FORWARD_URL=... FORWARD_USERNAME=... FORWARD_PASSWORD=...
/tmp/skill-eval --bin /tmp/evalbin --network <network id> --models sonnet,haiku --budget 10 --out /tmp/eval-out
```

Flags: `--skills a,b` to limit, `--max-cases N` per skill (a must-not-trigger case is always kept), `--arms with,without`,
`--parallel`, `--run-budget` (per session) and `--budget` (whole run; cases not started are reported as not run).
It writes `results.json`, the transcripts and `report.md`.

What it scores: **triggered as expected** (did the skill load when it should, and stay unused when it should not),
**expected behaviours met** (a judge reads the transcript against each line of `expected_behavior`), **answered** and
**grounded** (every fact in the answer appears in a tool result), plus cost and tool calls with and without the skills.
Suites marked `"procedure_only": true` (skills that only teach a procedure) are not scored for grounding, because they have
no tool output to ground an answer in.

The cases name real things on the Forward Test Drive network (devices such as `atl-ce01`, snapshots 1175662 and 1175663), so
they need a network with that data; a case that depends on something Test Drive lacks (a change set) says so in its `note` and
expects `unknown`.

Forward's own agent evaluations (143 tasks, with a judging rubric per set) are in [../eval](../eval); score those the same way
once the Copilot uses the skills.
