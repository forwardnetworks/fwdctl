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
- dashboards and scorecards

## credentials

CLI, HTTP and SNMP credentials for a network, each with `credential_type`, its id and name, and the ids of any linked privileged-mode password. The password, key, community string and privacy/authentication keys are never returned. Which devices use a credential is in `inspect-collection` view `config`. Use the id, not the name, when another object refers to it.

## jump_servers and proxies

Host, port, user and id. A jump server's password or SSH key, and a proxy's credentials, are not returned.

## endpoint_profiles and collection_settings

Every endpoint profile of any type, used or not (`inspect-collection` view `config` shows only those endpoints use), and the organization's collection settings: the device collection timeout, retries and their delays, authentication and scan rates, per-device concurrency. An unset field means Forward's default.

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

## integrations

ServiceNow settings, Infoblox instances and, with `network_id`, Rapid7 sources. Passwords are removed.

## licensing, backups, saml

Licenses for the current organization without the signed key; backup settings for internal and S3 storage and the last scheduled backup, without access or secret keys; SAML status without identity-provider certificates or metadata. These usually need an organization administrator.

## organizations and cve_index

Organizations the login can see, and the vulnerability index's metadata (created, size). `inspect-environment` reports the index's age; `edit-platform` syncs it.

## dashboards and scorecards
Both read Forward APIs it has not published, so the shape may change and either can answer 404 on an older build (that is "unknown", not "none"). `dashboards` lists the network's custom dashboards with the widget layout as Forward stores it; the built-in defaults are not listed. `scorecards` returns the scorecard definitions and, when the network has a processed snapshot, each scorecard's score on the latest one (Forward computes them for the organization's license tier). Trends over time and the Excel checks report exist in the SDK but are not read here yet.
