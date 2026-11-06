# edit-checks: inputs

`network_id`, `action` (`create`, `deactivate`, or a ServiceNow incident action, below). For `create`: `definition` (Forward's check definition: a predefined check type, or an
NQE query), optional `name` (NQE checks only: Forward names a predefined check itself and answers 400 if a name is sent, so a name with a predefined definition is refused in the dry run), `note`, `tags`, `priority`, `persistent`. For `deactivate`: `check_id`. Optional for both: `snapshot_id`
(default the newest processed one), `apply` (default false). Find predefined types and existing checks with
`inspect-checks`; validate an NQE query with `validate-nqe-query` first.

An intent definition (`checkType` Reachability, Isolation or Existential with `filters`) is screened with the SDK's check builders before anything is sent: a Reachability check with headers on its `to` end, a `bypass` or `flowTypes` is refused, because Forward accepts it and then fails it at run time (only ERROR). Filters the SDK cannot read are passed through unchanged. (Forward's behaviour read from its source by the SDK session, not run live.)

## ServiceNow incidents of a check

Preview routes, read from Forward's source (SDK v0.8.18), not yet run against a live ServiceNow. Each is a dry run unless `apply`, and reads the check and the incidents already linked to it first.

- `raise_incident`: `check_id` and `incident: {summary, impact, urgency, details?}`, impact and urgency HIGH, MEDIUM or LOW (ServiceNow's 1, 2, 3; Forward would store any other number as LOW, so others are refused). Needs `confirm` = the check id. Forward raises one only for an enabled check that is FAIL in this snapshot and every later one; a check that is not FAIL here is refused before anything is sent. If the call fails Forward may still have created the incident: list them before retrying.
- `link_incident` / `unlink_incident`: `check_id` and `incident: {number}`; each is the other's undo. Unlinking leaves the incident in ServiceNow.
- `close_incident`: `incident: {number}`, `confirm` = the number. It closes the record in the org's ServiceNow; there is no reopen here. Forward refuses to close an incident still linked to a check, so a linked one is refused before anything is sent: unlink it first.
- `refresh_incidents`: Forward re-reads the state of every incident linked to the snapshot's checks; ServiceNow is not changed.

Listing needs VIEW_CHECK_INTEGRATIONS and VIEW_CHECKS; the changes need OPERATE_CHECK_INTEGRATIONS. All go through the deployment's outbound connections to the org's ServiceNow integration (`inspect-platform` area `integrations`). On a predicted snapshot Forward allows them only when the org property PREDICT_UNSANDBOXED is on. Read the incidents with `check-network-compliance` view `incidents`.
