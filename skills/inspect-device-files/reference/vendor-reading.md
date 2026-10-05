# Reading vendor config text: why a missing line is not an answer

Read before saying a feature is off, a rule is absent, or a setting is missing from a device's text. General vendor behaviour, not checked against this repository's code: confirm on the device's own text and say which file you read.

- **A feature gate hides the config.** NX-OS configuration for a feature does not exist unless `feature <name>` is enabled, so absent lines may mean the feature is off. Check the gate first.
- **Group-supplied config is not inline.** Junos `apply-groups` supplies configuration that is not in the stanza you are reading. Search the groups before saying a setting is missing.
- **Dormant objects.** An ASA ACL does nothing without an `access-group` applying it. A disabled FortiOS policy is still in the file. Say "present but not applied" when that is the case.
- **Partial views.** Managed firewalls (a management server pushing a rulebase) can leave the device text without the rulebase, so "no rule allows X" cannot be claimed from it. A firewall with several contexts may show only one view. Say which part the file covers.
- **Spelling differs.** The same setting has different keywords per vendor. Search with the alternatives, and say which spellings you tried.

When nothing matches, the answer is **unknown** with the files read and the patterns tried, not "not configured".
