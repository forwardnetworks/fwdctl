---
name: compare-device-config
description: Shows which devices' config files changed between two snapshots and the lines added or removed on one device. Use when asked what changed in a device's config or which were reconfigured.
compatibility: Needs the fwdctl binary on PATH and FORWARD_URL, FORWARD_USERNAME and FORWARD_PASSWORD in the environment.
metadata:
  cluster: "change"
  summary: "config lines changed between snapshots"
  maturity: "3"
  tools: "snapshots, devices, diffs"
---

# compare-device-config

## Intent

Answer "what changed in the configuration" from the text the devices produced. Forward has no route that returns a
diff, so this skill asks Forward which files changed, downloads both versions and compares them. Use
`analyze-blast-radius` for the size of a change and `compare-nqe-results` for modeled data; use this for the lines.

## Inputs

`network_id`, `before_snapshot_id`, `after_snapshot_id`. Without `device`, it lists every device whose files changed.
With `device`, it shows the lines added and removed in one file (`file` picks it; default the first changed file).
Optional: `file_type` (`CONFIG` default, `STATE`, `CUSTOM`), `max_lines` (default 40 per direction, at most 200),
`no_redact`. See `schema.json`.

## Procedure

1. Both snapshots must be processed. Otherwise `unknown`.
2. Ask Forward which files changed. No `device`: return the changed devices and their files.
3. With `device`: download the file at each snapshot (first 4 MiB) and compare the lines.
4. Decide:
   - Lines differ: **ok**, with the added and removed lines and their line numbers.
   - Forward lists the file as changed but no line differs: **ok**, and the limits say only ordering or whitespace
     changed, or the change is past the size cap.
   - Device not in the changed list: **ok**, no changed file; a device missing from a snapshot looks the same, so
     the limits point at `inspect-inventory`.
   - Neither side could be read: **unknown**.
5. Lines are compared as a set: a line that only moved is not reported. Secrets are redacted best effort, so a
   changed secret shows as identical text on both sides.

## Evidence

One `config` item: device, file, counts, and the added and removed lines with numbers.

## Output

The envelope in `schema/skill-result.schema.json`.

## Next actions

`inspect-device-files` to read around a change, `verify-change` to judge it.

## Running this skill

Give the inputs as one JSON object on stdin: `echo '{"network_id": "<id>"}' | fwdctl run compare-device-config`. It needs the
`fwdctl` binary and `FORWARD_URL`, `FORWARD_USERNAME`, `FORWARD_PASSWORD` in the environment. Read `status`
(`ok`, `failed`, `unknown`, `error`) and the `limits`; `unknown` is never a pass. Inputs are listed above and in `schema.json`.
