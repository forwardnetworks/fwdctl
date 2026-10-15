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

An AWS setup can be collected through a C2S Access Portal credential instead of plain assume-role. `inspect-collection` (view config) marks it `cap_credential`, shows the portal's URL (`cap_server_url`) and whether a CA is set (`cap_ca_set`) with its earliest expiry (`cap_ca_not_after`, `cap_ca_days_left`, `cap_ca_count`; a limit warns at 30 days and when it has expired; the client certificate's expiry cannot be read) ( a login without the device-credentials permission gets a limit instead, and the client certificate is never shown), and lists `c2s_accounts`, the accounts Forward collects. Read from Forward's source by two other sessions, not run against a live setup:

- **The per-account filter for a CAP setup is the portal account list (`c2s_accounts`), not the enabled flags in `member_accounts`.** Those flags are used only for plain assume-role accounts.
- **An empty list is not "collect everything".** Collection requires at least one pinned account: it rejects an empty list before it asks the portal, so the AWS snapshot comes back incomplete. The setup test and account discovery do treat empty as no filter and request credentials for every portal account. The GUI normally pins every discovered account, so an empty list comes from a legacy or API-created setup. `inspect-collection` says so in `c2s_accounts`.
- **The same error line comes from three places.** "Exception during C2S Access Portal credentials request for ..." is logged by the collection step AND by account discovery AND by the setup's connectivity test, and discovery requests credentials for every portal account on purpose each time someone opens the account drawer or runs a test. So an HTTP 400 for an unknown account does not show that a collection caused it: correlate the log time with a collection task versus a drawer or test action, and check that `c2s_accounts` is not empty.
- **One failing account or the whole collection depends on which request failed.** A failed credentials request for one account fails that account. A failed request for the portal's account list is rethrown and drops the whole collection.
- **Why the UI cannot fix it.** The drawer's Save needs a passing connectivity test whenever the account selection changes (only no error, or an authorization warning, counts), and the setup test reports the first error across the pinned accounts, so one failing account can block saving the very selection that would exclude it. `edit-source` `cloud_account` `update` with `c2s_accounts` replaces the portal list without that gate (a dry run first), and `collect` switches the whole setup. `accounts_enabled` is refused for a CAP setup because it would report success and change nothing that is collected.
- **A wrong account id or role is not an error.** A pair the portal does not offer makes the collection filter match nothing: AWS yields no data and the result looks like a thin snapshot. The dry run lists pairs that are not in the current list and are not checked against the portal.

What the tool can do: show whether the setup is CAP and which accounts it pins, show each account's last error where Forward holds one (do not assume the portal's text is in it), and read the collection log for the text. For anything else, send Forward support the setup name, the account ids, the error text and the snapshot ids; a customer that cannot share those can share counts, error classes and timestamps instead.

State what you could read and what you could not. The status of a cloud collection you did not read is **unknown**, not healthy.

## Finding these failures: view cloud

`investigate-collection-failure` view `cloud` scans the snapshot's ERROR log and rolls the cloud failures up by class, with the accounts each names and when it first and last appeared:
- `cap_credentials_http_400` (and other `cap_credentials_http`): a C2S Access Portal credentials request failed for the named account. Compare the time with a collection; discovery and the setup test write the same line.
- `cap_portal_certificate`: a TLS or certificate error talking to the portal (expired, not yet valid, untrusted). Every account fails at once, which is how a whole AWS collection drops after a portal certificate expires. `CERTIFICATE_CHECK_FAILED` is the collection error for an expired or untrusted portal server certificate; Forward gives no date for that certificate. The CA's expiry IS shown by `inspect-collection` view config (`cap_ca_not_after`, `cap_ca_days_left`, with a warning at 30 days and when expired); the client certificate's expiry cannot be read (its .p12 is encrypted under a password Forward does not return). Renew the certificate in the Forward UI, then run the setup test.
- `aws_assume_role_denied`, `aws_throttled`, `azure_auth`, `gcp_auth`, and `azure_<code>` such as `azure_SubscriptionNotFound` (the account ids are subscription ids: the subscription was deleted, moved tenant, or the credentials have no role on it).
No cloud line in the error log is **unknown**, not healthy: a collection that never started logs nothing. `inspect-inventory` kind `cloud_coverage` says which accounts were not collected and why the setup thinks so; this view says what the collector reported failing.

