# Comparing a device with its peers

Read when the question is "is this device configured like the others" or "which devices differ". `compare-device-config` diffs one device between two snapshots; for peers in one snapshot, use NQE over `device.files.config` (`author-nqe-query`, `reference/config-patterns.md`), not a loop of per-device file reads.

- **Report the outliers, not the conformers.** Say how many devices were compared, how many match, and list the ones that differ.
- **Drift hides one level deep.** Two devices can name the same route-map or prefix-list and define it differently. Compare the named objects (route-maps, prefix-lists, object-groups, ACLs) as well as the lines that call them.
- **Intended versus actual.** The configuration says what was intended; the parsed model says what the device does. A mismatch between them is itself a finding, worth naming.
- Say which devices had no config file collected: they are **not compared**, not "conforming".
