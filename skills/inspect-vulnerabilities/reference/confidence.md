# How far to trust a vulnerability verdict, and what to patch first

Read when a verdict is about to be stated as fact, or when ordering a patch list. The detection claims are general behaviour of version and configuration matching; the field names are in the SDK. Say which applied.

## What a verdict can and cannot say

- **Version-only detection** says the software version is in an affected range. It does not know about a patch applied without a version-string change, so a device can still look vulnerable after a fix. A new collection that flips the verdict is the proof.
- **Configuration-dependent detection** tests whether the vulnerable feature is configured, not whether an ACL stops anyone reaching it. A device can be vulnerable and unreachable. Pair it with a reachability check (`plan-vulnerability-response` step 4).
- **A "not vulnerable" from configuration can miss a non-standard syntax.** If a CVE is configuration-dependent and the platform's configuration analysis is unsupported, the verdict falls back to the version and is weaker. Say so.
- A fix or mitigation is confirmed only by a **new snapshot** showing the verdict change, not by the change request.

## Triage context

Judgement, not data from Forward. Treat a CVE on a firewall or an edge device as one step more urgent than the score alone, because that device sits on the boundary. A management-plane CVE on a device whose management interface a VPN or partner network reaches is effectively exposed. For a vulnerability with no fix yet, mitigate in this order: switch the feature off, restrict access to it, isolate the device. Say what you assumed about the deployment.
