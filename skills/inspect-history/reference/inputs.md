# inspect-history: inputs

`network_id` and exactly one of `check_id` (find it with `check-network-compliance` view read) or `device` (a device name). Optional: `snapshots` (how many to read). See `schema.json`.


## Read before relying on a result

### Device configuration (`device`)

Instead of a check, give a `device`: the skill walks adjacent pairs of processed snapshots, newest first (at most N snapshots, one diff read per
pair), and stops at the first pair whose collected configuration files differ for that device. **ok** with the two snapshots and their times;
if no pair differs the change is older than the snapshots read. A change is attributed to the interval between two collections, never to a time
inside it. Fewer than two processed snapshots is **unknown**.
