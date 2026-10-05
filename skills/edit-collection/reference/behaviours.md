# Starting and stopping collection: what to expect

Read before starting a collection, or when one "did not start". **Unverified:** these behaviours come from a colleague's notes on one Forward build and are not reproduced here; the skill itself already refuses to start when one is running or the collector is down.

- **One collection at a time per network.** A second start is refused (a conflict), so check `inspect-collection` view `status` first; the skill does.
- **The task is not the snapshot.** A started collection returns a task id. The snapshot it produces has a different id and appears only when the collection finishes and is processed: "started" is not "done" (`inspect-snapshots` until PROCESSED).
- **A collector that is too old may not run newer command sequences** (custom command groups with `:::` separators). If a custom command group "ran on no devices", list the causes before blaming the device: the collector version, the device filter, a group imported read-only from a parent network in a workspace.
