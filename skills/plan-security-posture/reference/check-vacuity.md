# Checks that pass without testing anything

Read before relying on a Forward isolation or reachability check, or before staging checks for a change. **Unverified:** the behaviours below were measured on one network by a colleague and have not been reproduced here; confirm with a read-only path search on your own network before stating them as fact.

## A passing isolation check can be vacuous

An isolation check that only counts valid flows passes when no valid path exists, including when the destination was never reachable from the source at all. Before trusting a PASS:
1. Path-search the two endpoints (`investigate-reachability`) and see what the network actually does.
2. Confirm the check would **fail** if a real delivered path existed. A check that cannot fail tests nothing.
3. Say a PASS "holds for what the check tests", and name the scope.

The reverse also happens: an existential check that does not restrict itself to valid flows can fail on a blackholed path while a valid one exists. If a check is red and a plain path search is green, the check is aimed at something other than what you typed.

## Use real endpoints

Aim a check at a service or host address, not a router loopback or a firewall's own interface (a directly connected route proves nothing). A check pinned to a device name rather than a subnet becomes a device filter and can fail when traffic merely takes a different, still-working route: treat that as a sentinel, and say so.

## Checks for a change: cutover and invariant

When you stage checks for a change (`verify-change`, `plan-change-review`):
- A **cutover** check should fail before the change and pass after. If it already passes on the base snapshot, it proves nothing about the change.
- An **invariant** check should pass before and after, for what must not break. Add a drain check (traffic left the old path) as well as an arrival check (traffic is on the new one).
- Derive checks from the design intent the person gave, not from route-map names or descriptions. Where the design and the network disagree, report that as a finding.
- Record each check's result on the base snapshot first; that is the comparison the after-result needs.
