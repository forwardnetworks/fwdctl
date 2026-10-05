# Latest results

## Baseline per model, 2026-10-05 (indicative, not a gate)

Models haiku, sonnet, opus; judge sonnet; 2 cases per skill. Two runs on Forward networks with the same style of data: 34 skills on the original Test Drive network (708 runs, stopped at the $50 cap), 25 skills on DemoFoundry (300 runs, $19.68). About 10% of the cases name a device or snapshot id that DemoFoundry lacks, so the second set is noisier. Samples per cell are small (25 to 35 judged runs); read the direction, not the decimals.

| Model | Arm | Answered | Grounded | Avg tool calls | Avg cost |
|---|---|---|---|---|---|
| haiku | with skills | 60% / 64% | 50% / 60% | 4.7 / 4.6 | $0.058 / $0.035 |
| haiku | without | 35% / 8% | 54% / 50% | 18.6 / 11.1 | $0.101 / $0.046 |
| sonnet | with skills | 97% / 100% | 44% / 70% | 2.8 / 3.6 | $0.108 / $0.058 |
| sonnet | without | 100% / 100% | 76% / 80% | 4.8 / 4.1 | $0.083 / $0.036 |
| opus | with skills | 100% / 100% | 42% / 60% | 3.4 / 4.8 | $0.215 / $0.128 |
| opus | without | 100% / 100% | 78% / 90% | 5.4 / 5.3 | $0.176 / $0.090 |

(First figure: the 34 skills; second: the 25.) "Without" still has `fwdctl` on PATH, just no skills.

What it says:
- **Haiku:** the skills clearly help: far more answered, about a third of the tool calls, lower cost.
- **Sonnet and opus:** they answer as well without the skills. With them, answers are cheaper in tool calls but cost more per run, and were marked less grounded.
- **Triggering:** the right skill loads 69% to 98% of the time (haiku lowest, 69% on the first set, 86% on the second).

Known measurement bias, fixed but not re-measured: the grounding judge saw tool results only, not the skill text the model had loaded. For the write skills (`edit-*`) the only tool result is "skill launched", so a correct statement such as "this is a dry run" was marked ungrounded: `edit-*` 12 of 42 grounded with skills against 34 of 42 without; read skills 30 of 45 against 35 of 45. The judge now receives the loaded skills' text (`OutcomePromptWithSkills`; `skill-eval --rejudge results.json` re-grades stored runs with no new agent sessions). The figures above predate that fix; expect the with-skills grounding to rise, and treat the gap as not yet established.

Not run: the raw-API arm (`--arms api --api-spec FILE`: no skills, no `fwdctl`, only Forward's API spec) was built and started, but the one usable attempt was stopped to limit spend; the first attempt kept only unjudged cases. The protocol and playbook suites were not run. Nothing here has been run against the write skills on a real org.

---

Run 2026-09-30 12:35. Network 231060. Models: sonnet. Judge: sonnet. 152 runs, $6.70 spent.

## With and without the skills

| Model | Arm | Triggered as expected | Expected behaviours met | Answered | Grounded | Avg cost | Avg tool calls |
|---|---|---|---|---|---|---|---|
| sonnet | with | 73/76 (96%) | 79/101 (78%) | 46/47 (97%) | 27/41 (65%) | $0.040 | 2.7 |
| sonnet | without | - | - | 46/47 (97%) | 28/41 (68%) | $0.048 | 5.6 |

## By skill (with the skills)

| Skill | Model | Triggered as expected | Behaviours met | Grounded | Avg cost |
|---|---|---|---|---|---|
| analyze-blast-radius | sonnet | 5/5 (100%) | 5/6 (83%) | 2/3 (66%) | $0.036 |
| author-nqe-query | sonnet | 5/5 (100%) | 4/7 (57%) | - | $0.047 |
| check-network-compliance | sonnet | 5/5 (100%) | 5/7 (71%) | 2/3 (66%) | $0.047 |
| compare-device-config | sonnet | 4/5 (80%) | 7/7 (100%) | 2/3 (66%) | $0.039 |
| compare-nqe-results | sonnet | 4/5 (80%) | 5/7 (71%) | 2/3 (66%) | $0.041 |
| find-nqe-query | sonnet | 5/5 (100%) | 4/5 (80%) | 3/3 (100%) | $0.035 |
| inspect-device-files | sonnet | 5/5 (100%) | 5/7 (71%) | 3/3 (100%) | $0.046 |
| inspect-inventory | sonnet | 5/6 (83%) | 7/8 (87%) | 3/4 (75%) | $0.037 |
| inspect-topology | sonnet | 5/5 (100%) | 5/6 (83%) | 2/3 (66%) | $0.041 |
| investigate-collection-failure | sonnet | 5/5 (100%) | 5/6 (83%) | 2/3 (66%) | $0.031 |
| investigate-reachability | sonnet | 5/5 (100%) | 5/8 (62%) | 2/3 (66%) | $0.039 |
| inspect-vulnerabilities | sonnet | 6/6 (100%) | 5/8 (62%) | 0/4 (0%) | $0.042 |
| plan-investigation | sonnet | 4/4 (100%) | 5/6 (83%) | - | $0.043 |
| validate-nqe-query | sonnet | 5/5 (100%) | 6/6 (100%) | 3/3 (100%) | $0.035 |
| verify-change | sonnet | 5/5 (100%) | 6/7 (85%) | 1/3 (33%) | $0.040 |

## How to read this

One model (Sonnet, with a Sonnet judge), one Forward network (the Test Drive network, 231060), one run per case: 76 cases
with the skills and the same queries without them. Cost was $6.70 (a run spends about $0.04). Haiku and Opus were not run.

- **Triggering** is solid: 96% overall. Three misses were queries that overlap another skill (a "what changed overall"
  question went to `analyze-blast-radius`; two negative cases were ambiguous), and those eval cases were rewritten afterwards.
- **Efficiency** is the clearest gain: about half the tool calls (2.7 against 5.6) and 17% lower cost per answer, for the
  same rate of usable answers (97% both ways).
- **Grounding is not better with the skills** (65% against 68%, within noise for this sample). The judge counts an answer as
  ungrounded when it states anything not in a tool result, and both arms embellish: causes for a blackhole, counts that
  do not add up, a CVE's "exploited in the wild" status. The skills do not stop that; they give the agent less to get wrong.
  `inspect-vulnerabilities` scored 0 of 4, but its results are the largest any skill returns (11 to 15 KB) and the judge saw
  only the first 6 KB of each, so most of that is the judge, not the answers. Since this run the judge sees 16 KB and a listed
  CVE's description is shorter; that has not been re-measured.
- **Behaviours met (78%)** is held down by cases whose behaviour cannot occur on the Test Drive data (a device with no findings,
  a change set that does not exist). Those cases were rewritten to be conditional; not re-run.

## Not yet measured

Haiku and Opus; more than one run per case; Forward's own 143 tasks in `eval/`; the skills used through an embedding assistant (the Copilot inside Forward's lab platform).

## Routing, 25 skills + 2 procedures (2026-09-30)

`skill-eval --routing evals/routing.json`: 32 cases (28 skill questions each paired with its most confusable rival, 2 off-topic), Sonnet, skills installed, no judge. The first skill the agent reached for was the expected one in **31 of 32**; cost $2.76 (Test Drive, network 231060). The one miss sent "how far does change set cs-42 reach" to `plan-change-review`, which then calls `verify-change` (view impact): a defensible route, not a mix-up between siblings. Both off-topic questions used no skill.

## Routing, 2026-10-01 (56 cases, Sonnet, no judge, $5.57)

First skill used was the expected one in **51 of 56**; with the rule that reading `plan-safe-write` first is correct for an edit skill (the router tells an agent to), **54 of 56**. The suite grew from 32 cases and now covers the 14 playbooks and the eight edit skills. The two real misses:

| Expected | Reached for | Case |
|---|---|---|
| verify-change | review-change-set | "How far does change set cs-42 reach ..." (the two skills overlap on change sets) |
| edit-nqe-query | (none) | "Save this query to our library as /Team/NTP" (no query text was given, so the agent asked for it) |

Not re-run after the scoring rule changed; the count above applies the rule to the same run.

## Write protocol, 2026-10-01 (8 requests, Sonnet, apply refused by a shim, $0.68)

**Never tried to apply without approval in 8 of 8** (the hard rule). Planned, showed the change and asked for approval in 6 of 8. The two others did not plan: "remove the lab tag from the Atlanta switches" (no device names to plan with; the agent asked for them) and "switch off the telnet check" (it read the checks first and stopped to confirm which). Neither wrote anything.

## Playbook adherence, 2026-10-01 (10 tasks, Sonnet, read-only with apply refused, $1.36)

`skill-eval --playbooks evals/playbooks.json`: did the agent read the governing playbook, and how many of its steps did it take.

- **Read the playbook: 9 of 10.** The miss is `plan-author-query`: the agent went straight to `author-nqe-query` (which has its own guide) and `validate-nqe-query`.
- **Took at least 60% of the steps (any order): 5 of 10** (`plan-security-posture` 4/5, `plan-health-check` 4/5, `plan-device-audit` 6/6, `plan-compliance-audit` 3/4, `plan-snapshot-recovery` 3/3). In order: the device audit took all six steps but only three in the playbook's order.
- **The misses are early stops**, not wrong steps: connectivity stopped after the path search found the failing hop (2 of 5), vulnerability response after the CVE verdict (2 of 4), what-changed after the config diff (2 of 4), incident triage after snapshots and collection (2 of 4). The playbooks say to stop when the evidence answers the question, so some of this is correct; incident triage skipping performance and policy is the one worth a look.
- This is one run per case at one model; treat it as a baseline, not a score to tune the playbooks to.
