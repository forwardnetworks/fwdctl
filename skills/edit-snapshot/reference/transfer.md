# edit-snapshot: export and import

## Contents
- export
- import

## export
Writes one snapshot to a ZIP on this machine. `definition.path` must be a new file in an existing directory: an existing file is refused, the ZIP is streamed to a temporary file owned by you (mode 600) and moved into place only when complete, and the result gives its size and SHA-256. `only_config` keeps the configuration files only. `include_devices` or `exclude_devices` (names or globs, not both) export a subset; Forward also adds devices related to an included one. An obfuscation key makes Forward replace sensitive data in the files; give it as `secret_file` (mode 600) or `secret_env`, never in the input, and keep it, because un-obfuscating needs it. `obfuscate_names` also replaces device names and needs the key. The file holds the network's configuration: keep it private. A snapshot still processing may be rejected.

## import
Uploads one or more ZIP files as a new snapshot of `network_id`; several files are merged. It changes no existing snapshot. `exclude_failed_devices` drops devices that failed while merging; `skip_processing` stores the upload without processing, and `action: reprocess` runs it later. `note` sets the new snapshot's note. The upload is streamed and has no time limit; the snapshot then processes in Forward, so read `inspect-snapshots` for its state. Undo is `delete` with `confirm` equal to the new snapshot id.
