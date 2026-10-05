# Evaluating skills (level 5)

There are two layers. **Level 4 to 5 today:** `cmd/skill-eval` runs each skill's cases against a real Forward network
with and without the skills and reports triggering, behaviour, grounding, cost and tool calls (see
[../evals/README.md](../evals/README.md) and the latest results in [../evals/RESULTS.md](../evals/RESULTS.md)). **Planted-fault
scenarios (below):** design only; they need lab capacity and live in an internal experiments bench.

A skill reaches level 5 when it is measured against scenarios with a known answer. The
internal experiments bench already plants faults in a lab, records what an investigator
concluded and how long it took, and judges the result against the planted truth. Each skill
gets scenarios in that format and a bench arm that runs the skill through the CLI adapter
(`fwdctl run <skill>`).

## Rubric for every model

Skills change often and models differ, so a skill change is judged against the same scorecard on each model that will use
it (the small, the middle and the large model: today `haiku`, `sonnet` and `opus`; add a model by adding a column). Run the
same cases on each (`skill-eval --models haiku,sonnet,opus`), and record the run in [../evals/RESULTS.md](../evals/RESULTS.md)
(model, date, network, the scores below). Compare a change to the **last recorded run of the same model**, never across models.

| Dimension | What is scored | Source | Bar |
|---|---|---|---|
| Routing | the right skill or playbook loads, and stays unused on a must-not-trigger case | `evals/skills/*.json`, `evals/routing.json`; offline `fwdctl which` | triggered-as-expected: no more than 5 points below that model's baseline; offline `which` must not get worse |
| Protocol | a write is a dry run first, waits for approval of that plan, applies once; `unknown` is never called a pass | `evals/protocol.json` | 100%: any miss blocks the change, on every model |
| Playbook order | the steps marked **Fixed** run in order and none is skipped; **Guided** steps keep the order; **Open** steps still say what was measured | `evals/playbooks.json` | every Fixed step present on every model; the rest no more than 5 points below baseline |
| Behaviour | the judge finds each `expected_behavior` line in the transcript | `skill-eval` | no more than 5 points below baseline |
| Grounding | every fact in the answer appears in a tool result | `skill-eval` | no more than 5 points below baseline; a fabricated fact is a finding on its own |
| Cost | tool calls and dollars per case | `skill-eval` | reported; a rise of more than 25% on a model needs a reason |

Three arms show what the skills add: `with` (skills installed), `without` (the model with `fwdctl` but no skills) and `api` (no skills and no `fwdctl`; only Forward's API spec, `--api-spec FILE`, and the credentials, so the model calls the API itself). `with` against `api` is the skills' total value; `with` against `without` is the skills' context alone.

How to read it: a regression on the smallest model usually means the skill leans on judgment that playbook steps should
fix; add detail or move the step from Open to Guided or Fixed. A regression only on the largest model usually means the
instructions over-constrain it. A new model starts with a recorded baseline; its bars are relative to that.

## Degrees of freedom

Each playbook (`plan-*`) gives every numbered step one level in its `## Freedom` section, defined in `plan-investigation`:
**Fixed** (exact skills, order and gates; every write is Fixed), **Guided** (the order is kept, the inputs are chosen),
**Open** (the goal is given, the approach is the model's). The more fragile or irreversible a step, the lower its freedom.
A structure test (`TestEveryPlaybookStepHasOneDegreeOfFreedomAndWritesAreFixed`) fails a playbook with an unlabelled step or a
write that is not Fixed. When a model fails a step in the rubric above, change the level or the wording of that step, not the
model's instructions elsewhere.

## Scenario per skill

| Skill | Planted fault | Expected result | Negative control |
|---|---|---|---|
| `investigate-reachability` | ACL denies TCP/443 on a firewall | `failed`, classification `security_denied`, the firewall as last hop | same lab, no fault: `ok` |
| `verify-change` | change set that isolates a site | `failed` against an expectation of delivered | change set that keeps connectivity: `ok` |
| `analyze-blast-radius` | change touching a known set of areas | those areas and the isolated pair count | identical snapshots: no observable difference |
| `investigate-collection-failure` | wrong credential on N devices | `failed`, category `credentials`, N devices | healthy collection: `ok` |
| `check-network-compliance` | one device violating a known check | `failed` naming that device | compliant lab: `ok` |

## What to measure

Correct status, correct classification, correct device, tool calls, bytes retrieved, and time to
conclusion. The operation log each result carries (`operations`) supplies calls and bytes, so the
same run feeds the API-gap report: where an agent needs many calls or large payloads to answer
one question.

## Ground rules

- Every scenario has a **positive and a negative control**. A skill that says `failed` on the
  healthy lab, or `ok` on the faulty one, is wrong; `unknown` on either is a finding about the data.
- Scenario definitions live in the blueprints experiments corpus, not in this repo's code.
- Labs are expensive: check node CPU requests before booting one.
