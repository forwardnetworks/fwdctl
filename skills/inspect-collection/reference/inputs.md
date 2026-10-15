# inspect-collection: inputs

`network_id`. Optional: `view` (`status`, the default, or `config`), `limit`, `offset`, `data_file` (view config: an exact data-file name, to also read its inferred schema), `collector` (view config: only the devices pinned to one collector, `101291` or `C101291`, or `none` for the unpinned ones), `names_only` (view config: devices as compact `[name, collector]` pairs, up to 20,000 per page), `include_content` (with `data_file`: also the first 2000 characters of the file, unredacted; off by default because a file can hold secrets), `data_connector` (view config: an exact data-connector name, to also read its endpoints and last test result).

**Waiting.** `wait_seconds` (view status, at most 120) polls every 5 seconds while a collection runs and returns when it finishes or the time is up, so one call after `edit-collection` apply reads the outcome instead of polling by hand. A running collection's finding names the task, how long it has run, how many sources finished and a rough estimate of the rest (an estimate: sources are not equally slow).

**View config output, per device.** Each device row carries `name`, `host`, `type`, `port`, whether a CLI or HTTP credential is set, and `collect`. When a device is pinned to a collector it also carries `collector_id` (for example `C42`); `devices_by_collector` counts the pinned devices per collector id. A device with no `collector_id` uses the network's default collector. Pin or move one with `edit-source` (object `classic_device`, field `collectorId`).

## Reading a whole data file

`data_file` previews the inferred schema, and `include_content` the first 2000 characters. To read **every row**, query it: an attached data file is `network.extensions.<nqe_name>` (the `nqe_name` is on each `data_files` row), a record `{status: OK|MISSING|INVALID_DATA, value}`:

    foreach r in network.extensions.<nqe_name>.value select r

Run it with `fwdctl nqe run --network ID --file q.nqe`. It needs the file attached to the network and a snapshot collected since. Each `data_files` row also carries `created_at`/`created_by`, `updated_at`/`updated_by` and `uploaded_at`/`uploaded_by` when Forward reports them: how stale a file a producer should refresh is, and who last changed it.

**Paging the device list (view config).** Forward returns the device list in no fixed order, so each page is cut from the list sorted by name, and the result carries `device_list_digest` (a digest of every device name in that read) and `device_count`: pages with the same digest came from the same list, and differing digests mean devices were added or removed between the reads. `collector` and `names_only` read one collector's devices (or the unpinned ones) as compact `[name, collector]` pairs, 20,000 to a page, so a network of tens of thousands of devices is read in a few calls.

