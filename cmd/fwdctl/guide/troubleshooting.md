# Troubleshooting

| Symptom | Cause and fix |
|---|---|
| `error: FORWARD_URL ... is not set` | Export `FORWARD_URL`, `FORWARD_USERNAME`, `FORWARD_PASSWORD`. |
| `401` or `403` | Wrong credentials, or the login lacks access. An API token's access key and secret work as username and password. |
| certificate error | Forward uses a certificate your machine does not trust. Prefer adding the CA to the system store, or give fwdctl the CA file: `--ca-bundle FILE` or `FORWARD_CA_BUNDLE=FILE` (a PEM file; it replaces the system roots and verification stays on). `FORWARD_INSECURE=true` turns verification off (every result records it); it cannot be combined with a CA bundle. |
| a long query ends at 120 seconds ("context deadline exceeded") | One HTTP call may take 120s by default. Queries now run through Forward's asynchronous execution API by default (wait up to 10 minutes; `FORWARD_NQE_WAIT=20m` changes it). Only with `FORWARD_NQE_MODE=sync` (or `--sync`) is a query one request that the 120s limit can cut off; `syncfirst` is sync with an async fallback. `fwdctl nqe run --timeout 600s` (or `FORWARD_TIMEOUT=600s`) raises the per-call limit instead. |
| a command prints nothing for minutes, or an agent's tool runner moves it to the background | The first query on a very large snapshot (tens of thousands of devices) can take minutes while Forward loads it; later ones take seconds. `fwdctl run` prints `still running after ...` on stderr every 30 seconds so it is visibly alive. Give the command a longer tool timeout or run it in the background and read its output when it ends; `FORWARD_TIMEOUT` and `FORWARD_NQE_WAIT` bound the wait. |
| `unexpected EOF` on `POST /api/nqe` | Forward closed the connection, usually because the query produced or held too much (a big network, whole records selected). Filter to one vendor before iterating nested lists, select scalar fields, and page with `--limit`/`--offset`. See the gotcha "Large networks" in `fwdctl describe author-nqe-query gotchas`. |
| `504 Gateway Timeout: stream timeout` on a query that runs for minutes | The one-request (synchronous) form was cut off by a gateway. Runs are asynchronous by default now, which has no such limit; if you set FORWARD_NQE_MODE=sync or --sync, drop it. |
| `unknown` and the limits say no processed snapshot | Nothing has been collected, or the newest snapshot is still processing. Run `inspect-snapshots`, then `investigate-collection-failure`. |
| `unknown` after an empty result | An empty answer is not evidence. Read `limits`: the filter may match nothing, or the data may be absent. |
| `invalid input: $.x ...` | The input does not match the skill's schema. `fwdctl run <skill> --help` lists the inputs. |
| a skill name is rejected | `fwdctl list` shows the current names; retired names are aliases and still work. |
| `fwdctl nqe lint` says valid but Forward rejects the query | The checker says nothing where it cannot tell a type (imports, pattern captures). Run `validate-nqe-query`. |
| Claude Code shows no diagnostics for `.nqe` files | `fwdctl` must be on the `PATH` of the shell that started `claude`, and the plugin must be installed. Check `/plugin` for an `Executable not found` error. |
