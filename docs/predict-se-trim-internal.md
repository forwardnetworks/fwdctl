# Trimming Predict skills and fwdctl to what an SE needs (proposal, internal)

Status: proposal for Craig's review. Nothing here is changed yet. Internal only.

## Who and what
An SE running a customer PoC. Five jobs: (1) can Predict do this change, on this build, today; (2) stage it; (3) read what it did; (4) say plainly what is not modelled; (5) hand engineering a clean report when it is wrong. The corpus loop, the validator grammar checker and the measured-results tables serve engineering and QA, not these jobs.

## The Predict surface today
- `verify-change`: 8 views (verify, verdict, describe, impact, list, scope, preflight, paths); `SKILL.md` is 8.7 KB, the largest skill text.
- `edit-change-set`: 12 inputs (devices, bgp_advertisements, firewall_rules, run, apply, change_set_id, lifecycle, confirm and the rest).
- Also touched by Predict work: `edit-snapshot` (import, export, cancel), `inspect-bgp-neighbors` (direction in/out), `edit-network`, `inspect-jobs`.

## Proposal
1. **One source of truth for "what works".** `verify-change/reference/predict-limits.md` and the PoC doc's measured tables are hand-written and go stale within days. Feed them from the daily field report (Skyforge, native Go): per command form, per build, pass / diff / rejected. The skill text then says "see today's report" and keeps only rules that do not move (validator behaviour, what a dry run is, "unknown is never a pass").
2. **Shrink the always-read text, not the behaviour.** Move the verify procedure steps and the long per-view paragraphs of `verify-change/SKILL.md` into `reference/`; keep intent, the views in one line each, and next actions. Target about half the size. No input or output changes.
3. **Make the SE path the default path.** Four things stay front and centre: `scope`/`preflight` (can I), `edit-change-set` with `devices` / `bgp_advertisements` / `firewall_rules` and `run` (stage), `describe` and `impact` (read), `verify` with expectations (judge). Move the rest to reference only: `lifecycle` ops (rename, duplicate, delete, discard_device, restore_commit, predict), whole-config replacement, `paths`, `list` (candidates; check use first).
4. **Check before merging views.** `scope` and `preflight` both read effective flags and may be one view; confirm what differs before changing anything, and keep both names as aliases for two releases if merged.
5. **No SE page in fwdctl.** fwdctl is built for everyone, so there is no `fwdctl docs predict`. SE guidance (the five jobs, the commands, where today's field report is) lives only in the internal PoC runbook / an internal Predict skill, loaded only for a PoC. Public `--help` stays as it is, except that long per-view text moves to `reference/` (step 2).

## Rules this must keep
- No new top-level skill; no input removed (hide or move text only); aliases for two releases for any rename; always-loaded budget test stays green; descriptions within 190 characters.
- Tell Skyforge before any push (its build pins main); retired or renamed names are listed in the release notes and sent to Skyforge.
- No customer data; the field report carries counts and change types only.

## Order
1. Agree the report's machine-readable shape with Skyforge (asked 2026-10-20).
2. Trim `verify-change` and `edit-change-set` text into `reference/` (no behaviour change), release.
3. Write the SE section of the internal runbook (and an internal Predict skill if it earns one); no fwdctl docs page.
4. After a release or two of the report, replace hand-written limits with the report's data.
5. Only then consider moving inputs or merging views.
