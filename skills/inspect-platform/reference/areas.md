# inspect-platform: what each area shows and cannot show

Read the section for the area you are answering from. Every area removes secret values by field name before returning anything.

## Contents
- credentials
- jump_servers and proxies
- endpoint_profiles and collection_settings
- collectors and schedules
- cloud_setups
- locations and tag_definitions
- access_labels and api_tokens
- webhooks, banners, certificates
- integrations
- licensing, backups, saml
- organizations and cve_index
- org_properties
- dashboards and scorecards

## credentials

CLI, HTTP and SNMP credentials for a network, each with `credential_type`, its id and name, and the ids of any linked privileged-mode password. The password, key, community string and privacy/authentication keys are never returned. Which devices use a credential is in `inspect-collection` view `config`. Use the id, not the name, when another object refers to it.

## jump_servers and proxies

Host, port, user and id. A jump server's password or SSH key, and a proxy's credentials, are not returned.

## endpoint_profiles and collection_settings

Every endpoint profile of any type, used or not (`inspect-collection` view `config` shows only those endpoints use), and the organization's collection settings: the device collection timeout, retries and their delays, authentication and scan rates, per-device concurrency. An unset field means Forward's default.

`view: effective` (collection_settings only) answers "what is non-default or temporary?": one row per setting for the organization and for each collector (`scope`, `setting`, `value`, `default`, `source` set or default, `differs`), and the finding counts the differing ones. An absent timeout means the default timeout, not none. The defaults are Forward's compiled defaults as carried by the SDK; `commandDelayMs` and the on/off switches are shown only when set, because their defaults are not known here. To put a setting back, `edit-platform` area `collection_settings` sets it to the default value (it cannot clear a field, and it covers only some settings: see its reference); the organization properties that may also govern timeouts need Forward support authority (area `org_properties`).

## collectors and schedules

Collectors are organization-wide: name, status, connected, version, last connection. Collection schedules belong to a network (times or period, time zone, enabled); Forward does not return the next run. The organization's collection settings are read by `inspect-collection` view `status` and `investigate-collection-failure` view `slow`.

## cloud_setups

Cloud accounts (AWS, Azure, GCP, VMware), controller-managed setups (ACI, SD-WAN, Zscaler) and Mist setups for one network, each tagged with `setup_type`. Keys, private keys and passwords are not returned. A region's last connectivity test is in `inspect-collection` view `config`.

## locations and tag_definitions

Locations defined on a network, and the device tags defined on it (`edit-device-tags` applies them to devices).

## access_labels and api_tokens

Device access labels (organization-wide). API tokens: only the calling login's own, by name and dates; never the secret. Another user's tokens need the organization administrator role.

## webhooks, banners, certificates

Webhooks with their last test result (the signing secret and any URL token are removed), custom banners, and trusted certificates (bodies shortened).

**banners** reads everything the UI shows as a banner: the org's custom banners (colour is RED, ORANGE, YELLOW, GREEN or PURPLE), the operator's **system announcement** (SaaS only: on-prem has none and says so in the limits), and the org-wide **critical states** row: `active` (in the UI's priority order), the scheduled `maintenance_window` (SaaS), `reported` (the state names this Forward sent) and `unmodelled` (a state this build does not know by name). `UNLICENSED`, `LICENSE_EXPIRED` and `LICENSE_NOT_STARTED_YET` are not sent by Forward: they are derived from the org's license status, as the UI does. The free-trial banner has no route of its own: it shows for an org of type FREE_TRIAL with an ACTIVE license, with the days left from that license's expiry (`inspect-platform` area `licensing`).

## integrations

ServiceNow settings, Infoblox instances and, with `network_id`, Rapid7 sources. Passwords are removed.

## licensing, backups, saml

Licenses for the current organization without the signed key; backup settings for internal and S3 storage and the last scheduled backup, without access or secret keys; SAML status without identity-provider certificates or metadata. These usually need an organization administrator.

## organizations and cve_index

Organizations the login can see, and the vulnerability index's metadata (created, size). `inspect-environment` reports the index's age; `edit-platform` syncs it.

## org_properties

Every property the appserver defines for one organization (`org_id`, else the organization of `network_id`, else the only organization the login sees; Forward support permission is needed). Each row has the `effective` value and its `source`: `org` (set on the organization; `org_value`), `global_override` (a global setting), or `compiled_default`. No Forward route exposes the compiled default separately, so `compiled_default` means "not set on the org and not a global override", and the effective value is then the default. `deployment_default` is the global override if there is one, else the compiled default. A first row, `appserver_build`, gives the build, release and version of the Forward read. Filter with `name` (for example `predict_`). The default in the Forward source is not always the default a deployment runs: a deployment can pin a value in a platform layer, so on one live primary build `PREDICT_MODEL_OSPF`, `PREDICT_MODEL_CONFIG` and `PREDICT_MODEL_CROSS_PROTOCOL` read false by default although the source default is true. Trust the `effective` value and its `source`, not the source code. The NQE feature flags are properties here too (`name` filter `nqe_`, `cis_`, `peer_lldp`): `NQE_SECURITY_RULES_PANOS`, `NQE_SECURITY_RULES_FORTIOS`, `CIS_BENCHMARKS`, `NQE_ADDITIONAL_HOST_INFO`, `PEER_LLDP_CDP_CAPABILITY`, `NQE_LLDP_CHASSIS_ID`, `NQE_DEVICE_SERVICES`, `NQE_TRIM_BUILTIN`, `NQE_PARQUET`, `NQE_DASHBOARD_PANELS`. A flag that is off hides fields from that organization's NQE data model (`device.securityPolicy`, `device.services`, host ports); `fwdctl describe author-nqe-query reference/feature-flags.md` lists what each exposes and its default. A 403 "Forward support authority required" means this login is not enough: an organization administrator cannot read this area. Traps: some builds compile `PREDICT_MODEL_EXT_ADV` as true, so a missing org row reads true; a change to `PREDICT_MODEL_CONFIG` needs the snapshot reprocessed. To compare two organizations or two clusters, run it on each (with each login) and compare the rows by name; one call reads one organization of one Forward.

## dashboards and scorecards
Both read Forward APIs it has not published, so the shape may change and either can answer 404 on an older build (that is "unknown", not "none"). `dashboards` lists the network's custom dashboards with the widget layout as Forward stores it; the built-in defaults are not listed. `scorecards` returns the scorecard definitions and, when the network has a processed snapshot, each scorecard's score on the latest one (Forward computes them for the organization's license tier). `scorecard_trends` gives each scorecard's score over the last 90 days (at most 60 points): first, latest, min, max and the change, with the snapshot ids of the first and latest points. A point with no score is counted, not averaged. The Excel checks report exists in the SDK but is not read here.
