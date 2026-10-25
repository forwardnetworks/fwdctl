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

## What a live test found (reported by a field session; read-only calls against one internal build, 1.0.0-261024; not re-run here)

These differ from the published user doc or are missing from it. Treat them as observations of one build.
- **The server is session-based, not stateless.** `initialize` returns an `Mcp-Session-Id` header; `tools/list` without it answers HTTP 400 and a bogus id 404, so a client must send the id back on every call.
- **`nqe` can fail with a normal result.** A generated query can be invalid and the call still returns `isError: false` with `status: "INVALID_QUERY"` and an `errors` field. On success the result is `{query, summary, status: "OK", executionKey}`. Check `status`, not `isError`. The `summary` is approximate prose (it said "more than 40 devices" where `get_network_complexity` returned exactly 44).
- **Arguments** (the doc names them only for `get_paths`): `deviceName`; `interfaceName`; `vlanId` (integer); `vrfName`; `hostSpecifier`; `objectId`; `subnetAddress`; `cveId`; `prompt` (nqe, generate_nqe_query); `query` (search_docs). `get_paths` takes `dstIp` plus `srcIp` or `fromDevice`, and optionally `srcPort`, `dstPort`, `ipProto`; a call with neither `srcIp` nor `fromDevice` is rejected.
- **`get_device_vulnerabilities` returns NOT_VULNERABLE rows too.** Each row has a `result` (VULNERABLE or NOT_VULNERABLE): counting every row as an exposure overstates risk.
- **Errors:** a nonexistent network and a network with no snapshot both answer "No processed snapshot for network N" (so "no snapshot" and "no access" are not told apart); a bad `get_paths` call can include a server-internal class name; `get_cloud_object` with an unknown id answers only "The tool failed to execute"; empty lookups (`get_vlan`, `get_host`) return empty lists.
- **403 when the toggle is off (confirmed live by the same field session on that build):** `POST /api/mcp` (initialize) answers HTTP 403, JSON, body `{"message":"MCP is not enabled for this organization","url":"./api/mcp","status":"403"}`. Re-enabling restored all 14 tools with the same credentials, and the property then read value true, overridden true (before the round trip it read default true, overridden false): re-enabling leaves an explicit override, not the original unset state.
- The MCP pages were not live on the public docs site when checked.

Where these go (the owner of the MCP docs and tool behaviour) is not recorded here; any report to Forward is the person's call, not a skill's.
