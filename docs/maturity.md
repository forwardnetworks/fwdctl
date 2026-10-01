# Skill maturity

| Level | Name | What it adds |
|---|---|---|
| 1 | Tool wrapper | Calls an API, returns results. Not yet a skill. |
| 2 | Procedure | Understands an objective, calls several capabilities, returns a conclusion. |
| 3 | Evidence-based | Structured evidence and a deterministic conclusion where possible (this repo's phase 1 target). |
| 4 | Composable | Structured `next_actions`; participates cleanly in larger workflows. |
| 5 | Evaluated | Automated scenarios with known outcomes, performance measurements, regression testing. |

A skill's level is recorded in its `SKILL.md` frontmatter (`maturity:`) and must be
earned: a level-3 skill has fixture tests that validate its envelope and a check that
an empty result yields `unknown`.
