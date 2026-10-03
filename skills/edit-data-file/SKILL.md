---
name: edit-data-file
description: Uploads a CSV/JSON/XML/YAML/TEXT dataset for NQE to join, attaches or detaches it on a network, or deletes it. Dry run unless apply is true. Use when a query needs data Forward lacks.
compatibility: Needs the fwdctl binary on PATH and FORWARD_URL, FORWARD_USERNAME and FORWARD_PASSWORD in the environment.
metadata:
  cluster: "nqe"
  summary: "add a data file"
  maturity: "1"
  class: write
  effect: "org"
  secrets: "true"
  reversible: "false"
  tools: "data files"
---

# edit-data-file

## Intent

A data file is a dataset you upload (site ownership, a custom asset list, anything NQE does not collect on its own) so a query can join it. Once
a network carries it, a query reads it as `network.extensions.<nqe_name>`, a record `{status: OK | MISSING | INVALID_DATA, value}`. This skill
uploads a file, and attaches or detaches an existing one on a network. `inspect-collection` (view config) lists what exists and previews an
existing file's inferred schema; this skill only writes.

## Actions

| `action` | Inputs | Undo |
|---|---|---|
| `upload` | `name`, `file_type` (CSV, JSON, XML, YAML or TEXT), `content`, optional `nqe_name`, `description`, `headers` (CSV only) | `delete` the same name (clean while no network has attached it) |
| `attach` | `name`, `network_id` | `detach` the same file and network |
| `detach` | `name`, `network_id` | `attach` the same file and network |
| `delete` | `name`, `confirm` (must equal `name`) | **none**: Forward keeps the content nowhere else; read or download it first |

A file is **organization-wide**: `upload` is visible to every network once it exists, independent of any attachment. `attach`/`detach` change
only one network. Forward lower-cases the uploaded name; an empty `nqe_name` defaults to the name without its extension. `XLSX` is a binary
format and is not accepted here (upload it through Forward's UI); the STIG policy file has a fixed name and its own template and is refused by
both `upload` and `attach`/`detach`.

## What is refused, with the reason

A name that already exists (refused, not overwritten: `attach` the existing one instead); `headers` on a non-CSV type; `network_id` given with
`upload` (it is organization-wide); `nqe_name`/`description`/`file_type`/`headers`/`content` given with `attach` or `detach`; the STIG policy
file by either action; `attach` on a file already attached, or `detach` on one not attached (reported **ok**, nothing to change, not an error).

## Procedure

1. Read the organization's data files (`inspect-collection` has already shown them; this skill re-reads to check for a name clash or confirm
   the file exists before `attach`/`detach`).
2. Dry run: show the plan, the before and after, and the undo (or say there is none, for `delete`).
3. Apply: send the one call, then read back (the organization's file list for `upload`, the network's attached names for `attach`/`detach`) and
   report **failed** if the state does not match what was asked.

## Effect on snapshots

Attaching or detaching takes effect on the network's **next** snapshot onward: a query reads `status: MISSING` until one is collected after the
change. Uploading a file does not itself start a collection.

## Evidence

One `state` item (upload) or two (attach/detach: before and read-back).

## Next actions

`inspect-collection` to see the file and, once attached and a snapshot runs, `author-nqe-query` to read `network.extensions.<nqe_name>`.

## Running this skill

`echo '{"action":"upload","name":"sites.csv","file_type":"CSV","content":"site,owner\nnyc,ops\n"}' | fwdctl run edit-data-file` is the dry run;
add `"apply": true` after it is accepted. `echo '{"action":"attach","name":"sites.csv","network_id":"<id>","apply":true}' | fwdctl run edit-data-file`
attaches it.
