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
