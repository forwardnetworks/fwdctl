# investigate-collection-failure: parser failures, exceptions and neighbors

## Contents
- Parser failures
- view exceptions
- view neighbors

## Parser failures

 Forward marks a supported device PARSER_EXCEPTION whenever processing fails, even when it stored no exception, so an
empty exception list is expected and no message, class or line exists to read: the device list, its category and `collectionError`
(sometimes the root cause) and the raw files are what there is.

## view exceptions

**view exceptions** (takes `device`, `limit`, `offset`). The exceptions the collectors logged while collecting the snapshot, deduplicated by Forward: the first line of each stack trace, how many times, and which devices or cloud accounts.
It is where an error a collector **ignored** shows up: a collection can finish, and a cloud account read as collected, with an exception in the log (a quota or permission API call that failed, say). Needs the permission to view
collector exceptions (a network administrator); without it the answer is **unknown** with what is needed. The text can quote what the collector was doing: keep it out of public places.

## view neighbors

**view neighbors.** Lists the neighbours Forward sees but does not model, with discovery method,
This is ONE REST call with no paging; on a very large network (thousands of devices with many neighbours) it can run past the HTTP timeout (measured: timed out at 2 minutes on an
~11,000-device network). The error then says so and names `FORWARD_TIMEOUT` (for example `FORWARD_TIMEOUT=10m`) to raise it. `view summary` reads the same call but treats a slow or
failed read as a limit, not a failure to answer: it still reports what it could.
addresses and which devices see them, and marks each that a modelled device peers with over BGP
(`bgp_peer`, `bgp_sessions` with peer AS and state), BGP peers first: an unmodelled BGP peer is
usually the upstream or edge. `plan-synthetic-device` covers modelling one.

## Procedure

1. Resolve the snapshot. None at all: `unknown`.
2. If the snapshot is still processing, stop: `unknown`. Failure counts are incomplete until
   processing finishes, and an in-progress snapshot must not read as healthy or as failed.
3. Read the collector task if one was given. FAILED, TIMED_OUT or CANCELED is a failure;
   QUEUED or RUNNING means the collection has not finished (`unknown`).
4. Read the snapshot metrics. Group `deviceCollectionFailures` by category:

   | Category | Failure types |
   |---|---|
   | credentials | AUTHENTICATION_FAILED, AUTHORIZATION_FAILED, CONFIG_COLLECTION_UNAUTHORIZED, PRIV_PASSWORD_ERROR, KEY_EXCHANGE_FAILED, jump/proxy auth failures |
   | network_path | CONNECTION_TIMEOUT, CONNECTION_REFUSED, NETWORK_UNREACHABLE, jump/proxy connection failures |
   | device_session | IO_ERROR, SESSION_CLOSED, STATE_COLLECTION_FAILED, NO_SPACE_LEFT_ON_COLLECTED_DEVICE |
   | unclassified | anything else, including UNKNOWN |

   `deviceProcessingFailures` are parsing or modeling problems, not access problems.
5. List neighbours that are seen but not modeled (`missing devices`) and, when permitted,
   the processing exceptions. Exceptions need a permission the caller may lack; then they
   are reported as **not read**, never as "none".
6. Decide:
   - Any collection or processing failure, a failed snapshot or a failed task: **failed**.
   - Processed, devices collected, no failures: **ok**.
   - Nothing collected and no failure recorded, or still in progress: **unknown**. Zero
     devices with zero failures means nothing was measured.

