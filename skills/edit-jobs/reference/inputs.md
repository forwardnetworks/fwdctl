# edit-jobs: inputs

- `older_than_minutes` (default 20; 0 is a valid threshold, not "unset" -- it cancels matching jobs immediately): cancels an active job whose
  `longest_running_time_seconds` is at least this, OR, for a job sitting at `running: 0`, whose `duration_seconds` is at least this (queued the whole
  time with no worker ever dispatched to it -- `longest_running_time_seconds` cannot see this case).
- `network_id`: narrow to one network; omit to consider every network.
- `limit` (default 10): cancel at most this many jobs in one run, so a mistaken very-low threshold cannot sweep an unbounded amount of work.
- `apply` (default false): dry run unless true.
