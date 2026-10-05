---
name: inspect-device-files
description: Reads the raw configuration and command output collected from one device by listing files, reading a window or regex search. Use when asked what a device's actual config or show output says.
compatibility: Reading the skill needs nothing. To run its analyses against Forward: the fwdctl binary on PATH and FORWARD_URL, FORWARD_USERNAME and FORWARD_PASSWORD in the environment.
metadata:
  cluster: "inventory-topology"
  summary: "raw device config and output"
  maturity: "3"
  tools: "snapshots, devices"
---

# inspect-device-files

## Intent

Return the text the device itself produced, as collected: the running configuration and the output of the
commands Forward ran. Prefer `inspect-inventory` or an NQE query for questions about the modeled network; use this
when the question is about the literal text (a specific line, a vendor command output, a setting the model lacks).

## Inputs

`network_id`, `device` (exact name), `mode`:

| `mode` | Needs | Returns |
|---|---|---|
| `list` | nothing more | file names, sizes and, where Forward recorded it, the command |
| `search` | `pattern`; `file` optional | matching lines with line numbers; `context` lines around each (max 5), `max_matches` (default 10, max 50). With no `file`, every collected file of the device is searched (up to 100 files and 64 MiB): the result lists the files that matched with counts, and the first matching lines with their file name |
| `read` | `file` | a window of lines: `start_line` (default 1), `max_lines` (default 150, max 500) |

Always `list` first if you do not know the file name, or search with no `file` to find which of a device's files mention a setting. State the model does not hold (VXLAN and EVPN: remote VTEPs, the VNI map; route-maps and communities; vendor-only commands) is read from the device's own files this way, one device at a time. Prefer `search` to `read`: a config runs to thousands of
lines and every line returned costs tokens. Optional: `snapshot_id` (default newest processed). `pattern` is a Go
(RE2) regular expression. See `schema.json`.

## Procedure

1. Resolve the snapshot; it must be processed. Otherwise `unknown`.
2. `list`, or download the file (first 4 MiB at most) and read or search it in memory.
3. Decide:
   - Lines returned: **ok**, with the window and the file's total line count. If more remain, the limits give the
     `start_line` to continue from.
   - No matching line: **unknown**. The setting may be absent, spelled differently in this vendor's syntax, or in
     another file. It is never "not configured".
   - Unknown device or file, or an empty file: **unknown**, with the reason.
4. Secret values after keywords such as `password`, `secret`, `community`, `key` are replaced with `<redacted>` on a
   best-effort basis; the limits say how many lines changed. `no_redact` turns it off.

## Evidence

One `config` item: device, file, total lines, and the lines or hits with their line numbers.

## Output

The envelope in `schema/skill-result.schema.json`.

## Next actions

`check-network-compliance` to test a finding across the network, `inspect-inventory` for the modeled view.

## Running this skill

Give the inputs as one JSON object on stdin: `echo '{"network_id": "<id>"}' | fwdctl run inspect-device-files`. It needs the
`fwdctl` binary and `FORWARD_URL`, `FORWARD_USERNAME`, `FORWARD_PASSWORD` in the environment. Read `status`
(`ok`, `failed`, `unknown`, `error`) and the `limits`; `unknown` is never a pass. Inputs are listed above and in `schema.json`.

## Reference

Read the file you need, when you need it (where you cannot read files: `fwdctl describe inspect-device-files reference/<file>`).

- [reference/vendor-reading.md](reference/vendor-reading.md): why a missing line is not an answer (feature gates, groups, dormant objects, partial views). Read before saying something is not configured.
