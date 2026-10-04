# Snapshot retention policy

A network's policy says how many of its snapshots Forward keeps in each age band when the daily cleanup thins them. It is per network and on-premises only (Forward answers 404 to a change on the hosted service).

## Contents
- Bands and granularities
- Allowed combinations
- What is always kept

## Bands and granularities

`lastWeek` (0 to 7 days), `lastMonth` (7 to 30), `lastQuarter` (30 to 90), `lastYear` (90 to 365), `older`. Each takes a granularity: `ALL` keeps every snapshot, `ONE_PER_DAY`, `ONE_PER_TWO_DAYS`, `ONE_PER_WEEK`, `ONE_PER_TWO_WEEKS`, `ONE_PER_MONTH`, `ONE_PER_QUARTER` keep one per period, `NONE` keeps none. `enabled: false` keeps everything.

## Allowed combinations

Forward enforces these with a 400: `lastWeek` must be `ALL`; `lastMonth` is `ONE_PER_DAY`, `ONE_PER_TWO_DAYS` or `ONE_PER_WEEK`; `lastQuarter` is `ALL`, `ONE_PER_DAY`, `ONE_PER_WEEK`, `ONE_PER_TWO_WEEKS` or `NONE`; `lastYear` is `ONE_PER_DAY`, `ONE_PER_WEEK`, `ONE_PER_MONTH` or `NONE`; `older` is any of those plus `ONE_PER_QUARTER`.

## What is always kept

The 10 newest processed snapshots of the network (not forks, drafts or restored ones), every favorite, and predictions and forks with the snapshots they derive from. The organization property SNAPSHOT_AUTO_CLEANUP can switch cleanup off for the whole organization (Forward administrators only). `inspect-snapshots` with `retention: true` lists what the saved policy would delete at the next pass.
