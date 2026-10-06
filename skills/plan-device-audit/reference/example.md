# Worked example: "tell me about core-sw02"

Illustrative: the shape of a good run, with results shortened and every name made up.

**Question:** "Give me everything on core-sw02."

1. `inspect-inventory` kind `devices` -> Arista, model 7280, EOS 4.28, site `dc1`, tag `core` (snapshot `5120`).
2. `inspect-topology` kind `links` with `device` -> 6 links; one manual override to `core-sw01`.
3. `inspect-device-files` search for `snmp-server community` -> two matching lines (searched, not dumped).
4. `inspect-vulnerabilities` for the device -> 2 CVEs; one needs a feature that is configured.
5. `inspect-performance` -> no samples collected: **unknown**, not healthy.
6. `inspect-history` with `device` -> config last changed between `5104` and `5108`.

**Answer:** one short section per area, each ok, failed or unknown with its snapshot: identity ok, links ok (one manual), config ok (searched), exposure failed (1 relevant CVE), load unknown (no samples), history ok. It recommends and changes nothing.
