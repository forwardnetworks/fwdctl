---
name: plan-device-audit
description: Sequences the skills that profile a device or group: identity, links, config, CVEs, performance. Use when reviewing one device or site.
compatibility: Needs the fwdctl binary on PATH and FORWARD_URL, FORWARD_USERNAME and FORWARD_PASSWORD in the environment.
metadata:
  cluster: "inventory-topology"
  summary: "full picture of a device"
  maturity: "2"
  tools: "inspect-inventory, inspect-topology, inspect-device-files, inspect-vulnerabilities, inspect-performance, inspect-history, edit-device-tags"
---

# plan-device-audit

A procedure, not a skill that runs. Name the snapshot every fact comes from, and report what was not looked at.

## Steps

1. **Identify it.** `inspect-inventory` (kind devices) for the device or the group: name, vendor, model, OS version, location, tags. A name that is not found is **unknown**, not "no such device".
2. **Connections.** `inspect-topology` for what it connects to and its site, and for links added or ignored by hand.
3. **Configuration.** `inspect-device-files` for the text of the config or a show output (search it; do not dump it).
4. **Exposure.** `inspect-vulnerabilities` for CVEs on the device and what each one needs to apply.
5. **Load.** `inspect-performance` for CPU, memory, interface errors and utilisation, only where samples exist.
6. **History.** `inspect-history` with `device` for when its configuration last changed.
7. **Grouping.** If asked to group or label devices, `edit-device-tags` (follow `plan-safe-write`).

## The minimum

Before you answer, take at least: the identification (step 1) and at least two of connections, configuration, exposure, load and history. Stop earlier only when the evidence already answers the question, and say so.

## Answering

One short section per area, each ok, failed or unknown with its snapshot. Do not summarise a set of unknowns as clean. Recommend, never change a device.
