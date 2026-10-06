# Worked example: "bracket tonight's core upgrade"

Illustrative: the shape of a good run, with results shortened and every name made up.

**Question:** "Prepare before and after evidence for the core router upgrade."

**Before**
1. `inspect-snapshots` -> newest is 20 hours old, so `edit-collection` as a dry run (`plan-safe-write`); approved; applied; waited; new snapshot `5130` PROCESSED.
2. `edit-snapshot` action note -> "before upgrade CHG-123 (made-up id)"; dry run, approved, applied.
3. Expectations written down by the person: "branch-1 to dc-app tcp/443 delivered", "guest to dc-db blocked".

**After**
4. `edit-collection` again; `inspect-snapshots` shows `5131` PROCESSED (not merely started).
5. `compare-device-config` `5130` to `5131` -> `core-rtr1` and `core-rtr2` differ (planned). `dist-sw3` also differs: a finding.
6. `verify-change` with the saved expectations -> both held. `check-network-compliance` -> nothing got worse.
7. `edit-snapshot` note on `5131`: "after upgrade, verified".

**Answer:** per expectation, held or violated, with both snapshot ids; the unplanned change on `dist-sw3`; flows nobody listed were not covered.
