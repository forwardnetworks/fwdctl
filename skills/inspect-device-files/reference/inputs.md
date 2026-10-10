# inspect-device-files: inputs

`network_id`, `device` (exact name), `mode`:

| `mode` | Needs | Returns |
|---|---|---|
| `list` | nothing more | file names, sizes and, where Forward recorded it, the command |
| `search` | `pattern`; `file` optional | matching lines with line numbers; `context` lines around each (max 5), `max_matches` (default 10, max 50). With no `file`, every collected file of the device is searched (up to 100 files and 64 MiB): the result lists the files that matched with counts, and the first matching lines with their file name |
| `read` | `file` | a window of lines: `start_line` (default 1), `max_lines` (default 150, max 500) |

Always `list` first if you do not know the file name, or search with no `file` to find which of a device's files mention a setting. State the model does not hold (VXLAN and EVPN: remote VTEPs, the VNI map; route-maps and communities; vendor-only commands) is read from the device's own files this way, one device at a time. Prefer `search` to `read`: a config runs to thousands of
lines and every line returned costs tokens. Optional: `snapshot_id` (default newest processed). `pattern` is a Go
(RE2) regular expression.
