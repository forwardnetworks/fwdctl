# Applying link overrides with curl (customer side)

## Contents
- Read what is there (always first)
- Replace ALL of a snapshot's overrides
- Add or remove some
- Network-level, staged
- Check afterwards

Placeholders only: set `FWD_URL` (for example `https://forward.example.com`), `FWD_KEY` and `FWD_SECRET` (an API token's access key and secret), `NETWORK_ID` and `SNAPSHOT_ID` yourself; never paste real credentials into a ticket or a chat. A port is `<device> <interface>`; a link has no direction.
Sources: Forward's network-topology API reference and release notes 26.8 and 26.9, and the controller source. `fwdctl` itself uses the snapshot-scoped POST below.

**WARNING: a write to a snapshot's overrides (the first two calls) invalidates that snapshot, and the snapshots after it up to the end of its override range. They reprocess and their answers are unavailable until they finish. Do it outside demos and live customer work, and read the overrides back first.**

## Read what is there (always first)

```bash
curl -u "$FWD_KEY:$FWD_SECRET" -H 'accept: application/json' \
  "$FWD_URL/api/snapshots/$SNAPSHOT_ID/topology/overrides" > overrides.json
```

`overrides.json` is `{"present": [{"port1": "<device> <interface>", "port2": "<device> <interface>"}], "absent": [...]}`. Keep it: it is the undo.

## Replace ALL of a snapshot's overrides (snapshot-scoped, invalidates; the call in the customer thread)

```bash
curl -u "$FWD_KEY:$FWD_SECRET" -X PUT \
  "$FWD_URL/api/snapshots/$SNAPSHOT_ID/topology/overrides" \
  -H 'content-type: application/json' -d @overrides.json
```

`PUT` **replaces** the whole set: the body must hold every override you want to keep, so edit the file you just read (add the missing links, leave the others). A 200 with `{}` means Forward accepted it and has invalidated the snapshot, not that the link is in the topology; wait for the snapshot to be processed (a read meanwhile answers 409), then check the links.
Forward has deprecated this family of snapshot-scoped calls for removal in release 26.11.

## Add or remove some (snapshot-scoped, invalidates; what `edit-link-overrides` sends)

```bash
curl -u "$FWD_KEY:$FWD_SECRET" -X POST \
  "$FWD_URL/api/snapshots/$SNAPSHOT_ID/topology/overrides" \
  -H 'content-type: application/json' -d @edit.json
```

`edit.json` holds any of `presentAdditions`, `presentRemovals`, `absentAdditions`, `absentRemovals`, each a list of `{"port1": "...", "port2": "..."}`. A port must be a physical interface or a port channel that exists in that snapshot; otherwise Forward answers 400 "Illegal ports in link overrides".

## Network-level, staged (Forward 26.8 and later; does not invalidate by itself)

```bash
curl -u "$FWD_KEY:$FWD_SECRET" -X PATCH \
  "$FWD_URL/api/networks/$NETWORK_ID/link-overrides" \
  -H 'content-type: application/json' -d @edit.json
```

The same `edit.json` shape. The change is **staged**: existing snapshots are untouched and the network's next snapshot uses it. Read what is staged with `GET "$FWD_URL/api/networks/$NETWORK_ID/link-overrides?view=staged"` and what a snapshot has with
`GET "$FWD_URL/api/networks/$NETWORK_ID/link-overrides?view=snapshot&snapshotId=$SNAPSHOT_ID"`. `PUT` on the same path replaces the whole staged set (body `{"present": [...], "absent": [...]}`). To apply the staged overrides to an existing snapshot and every newer one now (this invalidates and reprocesses them):

```bash
curl -u "$FWD_KEY:$FWD_SECRET" -X POST \
  "$FWD_URL/api/networks/$NETWORK_ID/link-overrides?action=backdate&snapshotId=$SNAPSHOT_ID"
```

It needs the permissions to edit topology links and to invalidate snapshots. The staged operations answer 204 No Content.

## Check afterwards

```bash
curl -u "$FWD_KEY:$FWD_SECRET" "$FWD_URL/api/snapshots/$SNAPSHOT_ID/topology/overrides"
curl -u "$FWD_KEY:$FWD_SECRET" "$FWD_URL/api/snapshots/$SNAPSHOT_ID/topology"
```

The first should list the overrides you meant; the second lists the topology links (each link appears once per direction). A present override that is not among the links after processing was not applied; Forward's API does not say why.
