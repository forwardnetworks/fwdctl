# Tags: when they take effect and how they fail

Read before applying tags, or when a tag "did not appear". **Unverified:** these behaviours come from a colleague's notes on one Forward build and are not reproduced here; confirm on the dry run and the read-back, and say what you saw.

- **Timing.** A tag change takes effect for the next snapshot, or for the snapshot you name, and later snapshots carry it forward. An existing snapshot you did not name is not changed. Read `limits` in the dry run.
- **Names are matched without regard to case.** `Core` and `core` are the same tag: check for an existing tag before proposing a new spelling.
- **A bulk change is all or nothing on a bad device.** One device name that does not exist can fail the whole batch: the dry run lists the pairs that change, so check the device names there, and never retry a partly applied change.
- **A tag is not a group or an alias.** Tags label devices (`edit-device-tags`); an alias is a named set that checks refer to (`edit-alias`); a location is a place (`edit-network`). Choose by how the thing will be used.
