# Standards worth citing in a routing or security finding

Read when a finding should point at the standard behind it. These are IETF RFCs, which are stable references; vendor documentation pages move, so name the vendor's feature and let the reader search for it. A finding with no citation is still a finding: cite only when the standard says the thing.

| Topic | Reference |
|---|---|
| BGP operations and security (filtering, maximum-prefix, session protection) | RFC 7454 (BCP 194) |
| eBGP route propagation with no policy | RFC 8212 |
| BGP session authentication | RFC 2385 (TCP MD5), RFC 5925 (TCP-AO) |
| Generalized TTL security on eBGP | RFC 5082 (GTSM) |
| BGP graceful restart | RFC 4724 |
| Fast failure detection | RFC 5880 (BFD) |
| Ingress filtering, reverse-path checks | RFC 3704 (BCP 84) |
| OSPFv2 authentication | RFC 5709 (HMAC-SHA) |
| IS-IS authentication | RFC 5310 |
| EVPN (over MPLS) | RFC 7432 |
| EVPN over VXLAN and other network virtualization overlays | RFC 8365 |
| Segment routing architecture | RFC 8402 |
