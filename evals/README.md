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

## The standalone-help eval (evals/standalone)

Can an agent finish a task with only `fwdctl --help` (no skills, no docs), and does it stay grounded? `python3 evals/standalone/run.py --bin DIR_WITH_FWDCTL --model haiku|sonnet --arm without|with [--only id,id] [--repeat N] [--out DIR]` starts `fake_forward.py` (a tiny HTTP server with canned, synthetic JSON per `METHOD /path` in `routes.json`), runs each task in `tasks.json` with a headless `claude -p` that has Bash and Read only, and scores from the fake's request log and the final answer. No judge model. Unrouted requests answer 599 and are reported as `fixture_gaps`, so a missing fixture never looks like a model failure. A route may carry `variants` (the first whose `if` is a substring of the request path with its query, or of the body, wins; put specific ones first) and a `text` body served as plain text (a configuration file).

A task is `{id, prompt, tier, files?}` plus checks, all plain substring matches on the lowercased answer, so write them as affirmative phrases: a check like `answer_lacks: "all match"` also fails the correct answer "not all match".

| Check | Meaning |
|---|---|
| `answer_has`, `answer_has_any` | every phrase is in the answer / at least one is |
| `answer_lacks` | none of these phrases is (a secret, or a wrong conclusion) |
| `first_words_have`, `first_words` | the finding is in the first N words (default 40) |
| `max_words`, `max_tool_calls` | the answer or the work is no longer than this (register checks: soft, not a gate) |
| `must_call`, `must_not_call` | a request `METHOD /path` was (not) made |
| `must_call_before` | the first request happens before the second (evidence before the config read) |
| `must_write`, `body_has` | a write was made and some request of that kind carried every `body_has` string |
| `no_writes` | no request other than a read (an NQE run is a read) |

Tiers: `grounding` (answer from what was queried, not from the conversation or the user's claim; a missing thing is said to be missing; ask when the question is underspecified), `investigate` (a multi-step read ending in a concrete finding), `register` (brief, in the reader's vocabulary), the rest are tool use. A grounding or investigate failure is a blocker; a register failure is soft. One run of a model is noisy: use `--repeat 3` before reading anything into a single failure, and read the model's answer before blaming the model, because the check or the fixture is as often at fault.

