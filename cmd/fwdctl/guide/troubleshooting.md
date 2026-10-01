# Troubleshooting

| Symptom | Cause and fix |
|---|---|
| `error: FORWARD_URL ... is not set` | Export `FORWARD_URL`, `FORWARD_USERNAME`, `FORWARD_PASSWORD`. |
| `401` or `403` | Wrong credentials, or the login lacks access. An API token's access key and secret work as username and password. |
| certificate error | Forward uses a certificate your machine does not trust. Prefer adding the CA to the system store. `FORWARD_INSECURE=true` turns verification off (every result records it). |
| `unknown` and the limits say no processed snapshot | Nothing has been collected, or the newest snapshot is still processing. Run `inspect-snapshots`, then `investigate-collection-failure`. |
| `unknown` after an empty result | An empty answer is not evidence. Read `limits`: the filter may match nothing, or the data may be absent. |
| `invalid input: $.x ...` | The input does not match the skill's schema. `fwdctl run <skill> --help` lists the inputs. |
| a skill name is rejected | `fwdctl list` shows the current names; retired names are aliases and still work. |
| `fwdctl nqe lint` says valid but Forward rejects the query | The checker says nothing where it cannot tell a type (imports, pattern captures). Run `validate-nqe-query`. |
| Claude Code shows no diagnostics for `.nqe` files | `fwdctl` must be on the `PATH` of the shell that started `claude`, and the plugin must be installed. Check `/plugin` for an `Executable not found` error. |
