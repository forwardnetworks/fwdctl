# fwdctl

`fwdctl` runs Forward Skills: questions about a Forward network, answered with evidence. One binary, no runtime, no model. It
reads Forward's digital twin through Forward's API and prints one JSON result per question.

## Set up

Give it a Forward login in the environment:

    export FORWARD_URL=https://fwd.app
    export FORWARD_USERNAME=<an API token's access key, or a login name>
    export FORWARD_PASSWORD=<its secret, or a password>

TLS is verified against the system trust store. For a self-signed Forward set `FORWARD_INSECURE=true`: it turns verification off,
prints a warning and every result records it.

## A 30-second tour

    echo '{}' | fwdctl run inspect-networks               # which networks can I see, and their ids
    fwdctl list                                             # every skill with its description and inputs
    fwdctl run inspect-snapshots --help                     # what one skill answers, its inputs, an example
    echo '{"network_id": "<id>"}' | fwdctl run inspect-snapshots
    fwdctl describe plan-investigation                      # which skill answers which question

## Commands

| Command | What it does |
|---|---|
| `fwdctl run <skill>` | Run a skill. Inputs are one JSON object on stdin (or `--input FILE`). Prints the result JSON. |
| `fwdctl list` | Every skill: name, description, input schema. |
| `fwdctl describe <skill>` | A skill's procedure. `describe <skill> <file>` prints one of its reference files. |
| `fwdctl nqe lint [FILE]` | Check an NQE query offline: no Forward connection. See `fwdctl docs nqe`. |
| `fwdctl context nqe <question>` | NQE worked examples nearest a question. |
| `fwdctl context schema <term>` | Real NQE field names and enum values matching a term. |
| `fwdctl install claude` / `agents` | Put the skills where an agent loads them. See `fwdctl docs install`. |
| `fwdctl docs [topic]` | This guide. |
| `fwdctl version` | Release, commit and build date. |

## Exit status

| Code | Meaning |
|---|---|
| 0 | `ok`: the question was answered and nothing is wrong |
| 1 | `failed`: a finding (something is wrong), or for `nqe lint` a syntax error |
| 2 | `unknown`: it could not decide. **Never read this as a pass.** Read the `limits`. |
| 3 | `error`: the skill itself could not run |
| 64 | bad usage or input |

A script can branch on the code without parsing JSON.
