# Worked example: "do we meet our no-telnet policy?"

Illustrative: the shape of a good run, with results shortened and every name made up.

**Question:** "Show audit evidence that telnet is disabled everywhere."

1. `inspect-snapshots` -> snapshot `5120`, collected today.
2. `check-network-compliance` `view: read` -> 41 checks, 2 failing; none is about telnet.
3. `find-nqe-query` "telnet enabled" -> a saved query, `Q_telnet`. `check-network-compliance` with `nqe: {query_id: "Q_telnet", scope_query: <all devices>}` -> 3 of 118 devices violate (`sw-b2-04`, `sw-b2-05`, `rtr-lab-01`).
4. `inspect-history` for the failing check that matters -> failing since snapshot `5098` (9 days).

**Answer:** a table of rule, check, status, snapshot: telnet - NQE check - failed on 3 of 118 devices - `5120`. A rule with no check, or a check that matched nothing in its scope, would be `unknown`, not compliant. To keep the rule as a standing check: `edit-checks` (`plan-safe-write`). This is not a certification of any framework.
