# What Predict does and does not model

Read before saying a change set is safe, or before trusting a check on a predicted snapshot. **Unverified:** these limits were measured on one network by a colleague and are not reproduced here; the schema facts are checked against the NQE schema, the behaviours are not. Confirm with a read-only comparison before stating one as fact.

- **BGP RIB can be stale on a predicted snapshot.** Fields such as `activeRoute` and `adjRibInPost` may carry over from the base snapshot rather than being recomputed. An NQE check that reads them says nothing about the change. Read the forwarding table (`afts`) or run a path search on the predicted snapshot instead.
- **Advertisements are not policy.** Staging or removing a BGP advertisement cannot show a change whose effect comes from policy (a route-map, a local preference) re-selecting an existing route. A CLI change set that edits the policy is the thing to predict; say what was and was not modelled.
- **The device you cut may not re-select its own best path.** If a check starts from the device whose route you changed, test from the far side of the change as well.
- **Return path.** A check option that asks for a symmetric return path may be accepted without being asserted. Compare the two directions yourself (`investigate-reachability`, `reference/reading-paths.md`).
- **`unknown` stays unknown.** A check that needs additional processing is not a pass, and a predicted snapshot that is not yet ready is not an empty result: wait for it, then read.

Say in the answer which of these applied, and which flows were not tested.
