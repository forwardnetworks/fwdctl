# edit-org-property: inputs

- No `property`: **list** (read-only). Optional `match` (a substring of the name or its description), `limit` and `offset`. Each row: the property, its current value, whether the organization overrides it, the default,
  who may change it, and its risk.
- `property` and `value` (text: `true`, `42`, `ASYNC`), or `property` and `clear: true` (remove the organization's override so the default applies again).
- `confirm`: the property name again, **required to apply** a property classified `dangerous` or not classified at all.
- `apply` (default false).
- `org_id`: act on ANOTHER organization (list it, or set or clear one of its properties) through the Forward-support routes `/api/orgs/{id}/config`; global-config is never touched. It needs a Forward-support login: give it as a second token file, `fwdctl --support-token-file ~/support.netrc run edit-org-property` (or `FORWARD_SUPPORT_TOKEN_FILE`, or `support_token_file` in the config); the main login stays the organization admin's and is used for everything else. Without a support login the main login is tried, and a 403 is reported with this reason. The dry run names the target (id and name); apply needs `confirm` = `<property> <org_id>`. On a SaaS tenant most Predict flags (`predict_model_ospf`, `predict_model_config`, `predict_model_cross_protocol`) are not settable by the organization's own administrator (403); this is the route for them.


## Read before applying

### Guarded window (an experiment on how snapshots are processed)

For a property that changes how snapshots are **computed** (a parsing mode, for example), give `reprocess_snapshot_id`, `network_id` (that snapshot's network), `value` and optionally `window_minutes` (default 30, at most 240).
The window sets the value, reprocesses that ONE snapshot, waits for it to finish, and **always puts the original value back** (an override is set back to its value; no override is cleared), also when the reprocess fails,
the wait runs out or the call is cancelled. A restore that itself fails is reported loudly with the value to set by hand.

The property is **organization-wide**, so the window **refuses to start** while any snapshot in **any** network is UNPACKING, PROCESSING or RESTORING, or while an enabled collection schedule may fire before the window ends
(a time-of-day schedule is computed from its times, days and zone, the organization's zone when it names none; a periodic schedule is due at the network's last collection plus its period, and with no known last collection it
blocks). Anything that cannot be read blocks: unknown is never a pass. The dry run lists every blocker and writes nothing. After the window it reports any snapshot elsewhere that began processing during it. Use
`inspect-snapshots` with no `network_id` to see what is processing now.

### Refusals

A value of the wrong kind or outside Forward's range or enumeration; a property Forward does not define; a property only Forward support may change (**failed**, with who can); a dangerous or unclassified property
without `confirm`; a property whose value format is not modelled. An on-premises-only property is refused by Forward (403) on SaaS: the skill says so and does not retry another route.
- Per organization only, by design: this skill never writes Forward's global (deployment-wide) default. Forward also refuses a global write for an ORG-level property (about 115 of them) with a 403, and no route says which properties are ORG-level, so the per-organization write (`org_id`, or this login's own organization) is the way to change one. To see which organizations already override a property, read `inspect-platform` area `org_properties` with `view: overrides` and `name`. (Forward's behaviour read from source by the SDK session, not run live.)
