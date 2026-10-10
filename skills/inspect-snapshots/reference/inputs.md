# inspect-snapshots: inputs

`network_id`. Optional: `snapshot_id` (describe that one), `limit` (default 10, at most 100) and `offset` to page the list.


**Without `network_id`** the skill answers one organization-wide question: which snapshots are in progress (UNPACKING, PROCESSING, RESTORING; UNPROCESSED snapshots, which are not being worked on, are counted separately) in **any** network this login sees, with network, snapshot,
state and kind. A network that cannot be read is named and "nothing is processing" is then **unknown**, not ok. It reads every network's snapshot list just now: a moment in time, used before a change that must not overlap processing.
`snapshot_id`, `kind`, `limit` and `offset` need a `network_id`. With `retention: true` (and a `network_id`) it answers a different question: how Forward thins this network's snapshots (the policy per age band) and which snapshots the next cleanup would delete (the preview needs the DELETE_SNAPSHOT permission and deletes nothing; `limit` caps the list). Reading only: the policy can be changed on-premises only, and no skill here changes it.
