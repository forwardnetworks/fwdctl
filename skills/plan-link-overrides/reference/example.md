# Worked example: "our manual links disappeared"

Illustrative: the shape of a good run, with results shortened and every name made up.

**Question:** "The two manual links on fw-ext-01 are missing in the latest snapshot."

1. `inspect-snapshots` -> people use `5120`; the earlier snapshot `5090` still shows the links.
2. `inspect-topology` `compare_to_snapshot_id: "5090"` -> two present overrides exist in `5090` and not in `5120`.
3. `inspect-topology` `kind: link_overrides` -> both rows have `ports_exist: true` and `link_in_topology: false` in `5120`.
4. `edit-link-overrides` as a dry run on `5120` (`plan-safe-write`): two `add_present` entries, the undo is the two matching removals. Said plainly: writing overrides **invalidates `5120`**, so it reprocesses and is unavailable meanwhile. Approved; applied once; read back.
5. `inspect-snapshots` until `5120` is processed, then `inspect-topology` with the comparison again -> no difference.

**Answer:** what was missing, what was done to which snapshot, the proof, and what to expect: the next collected snapshot may start without these overrides. Not known: which side wins in a conflict, and who set them.
