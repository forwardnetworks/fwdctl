# edit-snapshot: export and import

## Contents
- export
- import

## export
Writes one snapshot to a ZIP on this machine. `definition.path` must be a new file in an existing directory: an existing file is refused, the ZIP is streamed to a temporary file owned by you (mode 600) and moved into place only when complete, and the result gives its size and SHA-256. `only_config` keeps the configuration files only. `include_devices` or `exclude_devices` (names or globs, not both) export a subset; Forward also adds devices related to an included one. An obfuscation key makes Forward replace sensitive data in the files; give it as `secret_file` (mode 600) or `secret_env`, never in the input, and keep it, because un-obfuscating needs it. `obfuscate_names` also replaces device names and needs the key. The file holds the network's configuration: keep it private. A snapshot still processing may be rejected.

## import
Uploads one or more ZIP files as a new snapshot of `network_id`; several files are merged. It changes no existing snapshot. `exclude_failed_devices` drops devices that failed while merging; `skip_processing` stores the upload without processing, and `action: reprocess` runs it later. `note` sets the new snapshot's note. The upload is streamed and has no time limit; the snapshot then processes in Forward, so read `inspect-snapshots` for its state. Undo is `delete` with `confirm` equal to the new snapshot id.

## A large import that ends in a timeout

A 502, 503 or 504 (for example "stream timeout") or a client timeout on a large upload does **not** mean Forward discarded the file: the snapshot can be created anyway (it appears as UNPACKING, then PROCESSING, then PROCESSED). The skill therefore waits briefly for a new snapshot before reporting. If one appears, it reports that snapshot (and says the call itself ended in an error and was not retried). If none appears, the result is `unknown`, not a failure: check `inspect-snapshots` for a new IMPORT snapshot and do **not** apply again until you have, because a blind retry adds a duplicate. Files over about 100 MB can outlast the default 120 s HTTP timeout; set `FORWARD_TIMEOUT=600s` (or more) first. The result states the bytes sent and how long the upload took. A 400-class refusal is still reported as a failed import.
