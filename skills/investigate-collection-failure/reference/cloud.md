# Cloud collection that fails or drops

Read when the cloud part of a collection (AWS, Azure, GCP) fails, drops accounts, or stops. Cloud collection does not run per device: it runs per cloud setup, per region (AWS) or subscription (Azure), and, for an AWS setup that discovers member accounts, per member account. The device views of this skill do not describe it.

## Steps

1. **What is configured.** `inspect-collection` with `view: config`: each cloud setup's `type`, `collect` flag, proxy, the last connectivity test per region or subscription (`last_test`, `failing_tests`), and for a setup that discovers accounts `member_accounts` (how many are on, how many are on and carry an error, and each account's id, name, enabled flag and Forward's last error; accounts that are on and erroring come first). If `collect` is false the setup is not collected at all.
2. **What was collected.** `inspect-collection` (the default status view): how many cloud accounts were not collected in the newest snapshot, and the newest snapshot's own result.
3. **Refresh the evidence.** Test the connection: `edit-source` with `object: cloud_account`, `action: test`, `name`. It is a dry run until `apply`, and the result is read afterwards from step 1 (it says the test is recorded by Forward, not returned).
4. **Act on the cause.**

| What you see | What it means and the next read |
|---|---|
| A region's `last_test` is `AUTHENTICATION_FAILED` | The provider rejected the credentials for that region. Rotate the secret (`edit-source` `cloud_account` `rotate`, secret from a file) or fix the role. Check the test timestamp: an old failure may not be current. |
| `last_test` is `never tested` | Nothing has verified the setup. Run the test (step 3) before drawing a conclusion. |
| A member account is on and carries an error | The last attempt for that account failed. Read the error text. If the account should not be collected, turn it off: `edit-source` `cloud_account` `update` with `accounts_enabled`. If it should, fix its role or credentials, then test again. |
| A member account is off and carries an error | On a plain assume-role setup the error is probably old: Forward keeps the last message. On a CAP setup (`cap_credential`) the enabled flags are not used, so an account shown as off may still be requested and the error can be current. |
| The error names a credentials request to an access portal (C2S Access Portal, CAP) with HTTP 400 for an unknown account name | See below. |

## C2S Access Portal (CAP) errors

An AWS setup can be collected through a C2S Access Portal credential instead of plain assume-role. `inspect-collection` (view config) marks it `cap_credential`, shows the portal's URL (`cap_server_url`) and whether a CA is set (`cap_ca_set`; a login without the device-credentials permission gets a limit instead, and the client certificate is never shown), and lists `c2s_accounts`, the accounts Forward asks the portal about. Read from Forward's source by the SDK owner, not run against a live setup:

- **The per-account filter for a CAP setup is the access portal account list (`c2s_accounts`), not the enabled flags in `member_accounts`.** Those flags are used only for plain assume-role accounts. With `c2s_accounts` empty, Forward collects every account the portal returns and requests credentials for each one: an account unchecked in the UI is still asked for.
- **The portal list can be set, the member flags cannot.** `edit-source` `cloud_account` `update` with `c2s_accounts` replaces the list (a dry run first; an empty list means every account the portal returns), and `collect` switches the whole setup. `accounts_enabled` is refused for a CAP setup because it would report success and change nothing that is collected.
- **One account failing or the whole collection failing depends on which request failed.** A failed credentials request for one account fails that account. A failed request for the portal's account list is rethrown and drops the whole collection. Which of the two an `HTTP 400 ... (UNKNOWN)` was cannot be told from source; the collection exceptions and collection log (`investigate-collection-failure` views `logs` and devices) are where the text is, when Forward kept it.
- A symptom pattern seen with CAP setups: accounts switched off still have credentials requested from the portal, a request fails with HTTP 400 for an account Forward cannot name, and the AWS collection drops on the following collections. Whether a given case is this cannot be decided from what this tool can read.

What the tool can do: show whether the setup is CAP and which accounts it lists, show each account's last error where Forward holds one (do not assume the portal's text is in it), and read the collection log for the text. For anything else, send Forward support the setup name, the account ids, the error text and the snapshot ids.

State what you could read and what you could not. The status of a cloud collection you did not read is **unknown**, not healthy.
