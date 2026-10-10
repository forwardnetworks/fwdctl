# inspect-collection: inputs

`network_id`. Optional: `view` (`status`, the default, or `config`), `limit`, `offset`, `data_file` (view config: an exact data-file name, to also read its inferred schema), `include_content` (with `data_file`: also the first 2000 characters of the file, unredacted; off by default because a file can hold secrets), `data_connector` (view config: an exact data-connector name, to also read its endpoints and last test result).

**Waiting.** `wait_seconds` (view status, at most 120) polls every 5 seconds while a collection runs and returns when it finishes or the time is up, so one call after `edit-collection` apply reads the outcome instead of polling by hand. A running collection's finding names the task, how long it has run, how many sources finished and a rough estimate of the rest (an estimate: sources are not equally slow).
