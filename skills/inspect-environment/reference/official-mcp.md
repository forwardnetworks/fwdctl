# Forward's official MCP server and where fwdctl fits

Read when someone asks whether Forward's MCP endpoint is on, how to connect a client, or which MCP tool corresponds to which fwdctl skill. **What is below about the endpoint comes from a field session's reading of Forward's documentation and source; it was not run from here. The toggle reading was run.**

## Is it on?
The endpoint is gated by the organization property `ENABLE_MCP_SERVER` (default `true`, changeable by an organization administrator or Forward support). `edit-org-property` with `match: "MCP"` (no `property`, a read) shows its value, default and whether it is overridden; setting it is the same skill with `property` and `value`, dry run first. Reported: when the property is off or the license is expired, `/api/mcp` answers 403 for every call.

## The endpoint (reported)
Streamable HTTP at `<forward-host>/api/mcp`, stateless, on SaaS and self-hosted. Auth is the per-user API token as HTTP Basic (access key and secret); there is no OAuth, so clients that need OAuth (claude.ai, Claude Desktop) cannot connect, and clients that take a static header (Cursor, VS Code and similar) can. Every call takes `networkId` (required) and `snapshotId` (optional, the latest processed snapshot by default). The MCP has no tool to list networks or snapshots.

## What the MCP carries and what it does not (reported)
Read-only tools: paths, device, interface, VLAN, VRF, host, cloud object, subnet locations, device vulnerabilities, a vulnerability, network complexity, plus NQE, NQE generation and documentation search (the last three need AI enabled for the org).

fwdctl covers what the MCP does not: listing networks and snapshots (`inspect-networks`, `inspect-snapshots`), checks and compliance (`check-network-compliance`), diffs (`compare-device-config`, `compare-nqe-results`), device files and configuration (`inspect-device-files`), topology (`inspect-topology`), collection (`inspect-collection`, `investigate-collection-failure`), Predict (`verify-change`, `edit-change-set`) and every change (`edit-*`, dry run first). Paths overlap with `investigate-reachability`, devices and interfaces with `inspect-inventory`, vulnerabilities with `inspect-vulnerabilities`, and NQE with the nqe skills and `fwdctl nqe`.

fwdctl does not print the endpoint URL or a ready-to-paste client config; build it from the Forward host in your login and the token's access key and secret, and never paste the secret into a result or a repository.
