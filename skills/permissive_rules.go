package skills

import (
	"context"
	"fmt"
	"net/netip"
	"sort"
	"strings"

	"github.com/forwardnetworks/fwdctl/fwd"
	"github.com/forwardnetworks/fwdctl/result"
)

// View permissive_rules of check-network-compliance: PAN-OS firewall rules that permit "any" source, "any" destination or any service, read from the ACL entries Forward models (the same entries,
// vendor allowlist and zone scope as shadowed_rules). A permissive rule is a fact about one rule, so order does not matter here, and a finding is a candidate to confirm: the ACL entries carry no
// application, user, URL category or schedule condition, and an any-address rule is often narrowed by those.

const defaultPermissiveShown = 25

// permissiveVerifiedOS are the vendors whose "any" is known to be modelled as 0.0.0.0/0, ::/0 or no address set at all (checked on live PAN-OS rules). It is separate from shadowVerifiedOS, which is about
// rule order and zone semantics; the rows query (shadowRowsQuery) filters on shadowVerifiedOS, so a vendor must be added to both.
var permissiveVerifiedOS = map[string]bool{"PAN_OS": true}

var (
	fullV4 = prefixInterval(netip.MustParsePrefix("0.0.0.0/0"))
	fullV6 = prefixInterval(netip.MustParsePrefix("::/0"))
)

// permissive tiers, worst first.
const (
	tierAnyAnyAny   = "any_source_any_destination_any_service"
	tierAnyToAny    = "any_source_any_destination"
	tierAnyService  = "any_service_from_or_to_any"
	basisUnbounded  = "no address set in the model (modelled as unconstrained)"
	basisAddressSet = "an address set that covers the whole address space"
)

var tierRank = map[string]int{tierAnyAnyAny: 3, tierAnyToAny: 2, tierAnyService: 1}

type permissiveEntry struct {
	device, name, action string
	index                int
	siblings             int
	tier                 string
	basis                string
}

func coversAll(set []ival, full ival) bool {
	return len(set) > 0 && ivalsWithin([]ival{full}, mergeIvals(set))
}

// classifyPermissive reads one row. ok is false when the entry is not a permit of a verified vendor that can be judged; tier is empty when it is a permit but not permissive.
func classifyPermissive(r map[string]any, skip *shadowSkip) (e *permissiveEntry, ok bool) {
	if os, _ := r["os"].(string); !permissiveVerifiedOS[os] {
		skip.Vendor++
		return nil, false
	}
	if d, _ := r["isDefault"].(bool); d {
		skip.Default++
		return nil, false
	}
	if a, _ := r["approximated"].(bool); a {
		skip.Approximated++
		return nil, false
	}
	if act, _ := r["action"].(string); act != "PERMIT" {
		skip.OtherAction++
		return nil, false
	}
	e = &permissiveEntry{action: "PERMIT"}
	e.device, _ = r["device"].(string)
	e.name, _ = r["name"].(string)
	n, nok := numOf(r["index"])
	if !nok {
		skip.Unparseable++
		return nil, false
	}
	e.index = int(n)
	if n, ok := numOf(r["siblings"]); ok {
		e.siblings = int(n)
	}
	s4, ok1 := parsePrefixes(r["src4"])
	d4, ok2 := parsePrefixes(r["dst4"])
	s6, ok3 := parsePrefixes(r["src6"])
	d6, ok4 := parsePrefixes(r["dst6"])
	proto, ok5 := parseRanges(r["proto"])
	tpSrc, ok6 := parseRanges(r["tpSrc"])
	tpDst, ok7 := parseRanges(r["tpDst"])
	if !(ok1 && ok2 && ok3 && ok4 && ok5 && ok6 && ok7) {
		skip.Unparseable++
		return nil, false
	}
	if (len(s4) == 0) != (len(d4) == 0) || (len(s6) == 0) != (len(d6) == 0) {
		skip.HalfFamily++ // one side of an IP version has addresses and the other none: the entry matches nothing in that version, so it is not read as 'any'
		return nil, false
	}
	allEmpty := len(s4) == 0 && len(d4) == 0 && len(s6) == 0 && len(d6) == 0
	src4, dst4, src6, dst6 := coversAll(s4, fullV4), coversAll(d4, fullV4), coversAll(s6, fullV6), coversAll(d6, fullV6)
	srcAny := allEmpty || src4 || src6
	dstAny := allEmpty || dst4 || dst6
	anyToAny := allEmpty || (src4 && dst4) || (src6 && dst6)
	svcAny := rngsWithin([]numRange{{0, 255}}, mergeRngs(proto)) && rngsWithin([]numRange{{0, 65535}}, mergeRngs(tpSrc)) && rngsWithin([]numRange{{0, 65535}}, mergeRngs(tpDst))
	switch {
	case anyToAny && svcAny:
		e.tier = tierAnyAnyAny
	case anyToAny:
		e.tier = tierAnyToAny
	case svcAny && (srcAny || dstAny):
		e.tier = tierAnyService
	}
	e.basis = basisAddressSet
	if allEmpty {
		e.basis = basisUnbounded
	}
	return e, true
}

type permissiveFinding struct {
	Device            string `json:"device"`
	Rule              string `json:"rule"`
	Index             int    `json:"index"` // the position of the rule's first permissive entry
	Tier              string `json:"tier"`
	Basis             string `json:"basis"`
	PermissiveEntries int    `json:"permissive_entries"`
	Entries           int    `json:"entries,omitempty"` // entries of the device that share the rule's name, when the rule is named and the query counted them
}

// findPermissive groups entries by rule (device and name; an unnamed entry stands alone) and reports each rule that has a permissive entry, at its worst tier.
func findPermissive(entries []*permissiveEntry) []permissiveFinding {
	type key struct{ dev, name string }
	by := map[key]*permissiveFinding{}
	var order []key
	for _, e := range entries {
		if e.tier == "" {
			continue
		}
		k := key{e.device, e.name}
		if e.name == "" {
			k.name = fmt.Sprintf("#%d", e.index)
		}
		f, seen := by[k]
		if !seen {
			f = &permissiveFinding{Device: e.device, Rule: e.name, Index: e.index, Tier: e.tier, Basis: e.basis}
			if e.name != "" {
				f.Entries = e.siblings
			}
			by[k] = f
			order = append(order, k)
		}
		f.PermissiveEntries++
		if e.index < f.Index {
			f.Index = e.index
		}
		if tierRank[e.tier] > tierRank[f.Tier] {
			f.Tier, f.Basis = e.tier, e.basis
		}
	}
	out := make([]permissiveFinding, 0, len(order))
	for _, k := range order {
		out = append(out, *by[k])
	}
	sort.SliceStable(out, func(i, j int) bool {
		if a, b := tierRank[out[i].Tier], tierRank[out[j].Tier]; a != b {
			return a > b
		}
		if out[i].Device != out[j].Device {
			return out[i].Device < out[j].Device
		}
		return out[i].Index < out[j].Index
	})
	return out
}

// permissiveRules is view permissive_rules.
func permissiveRules(ctx context.Context, s *fwd.Session, in complianceInput) (result.Result, error) {
	snap, err := resolveSnapshot(ctx, s, in.NetworkID, in.SnapshotID)
	if err != nil {
		return result.Result{}, err
	}
	cx := fwd.Context(in.NetworkID, snap)
	if !fwd.IsReady(snap) {
		return result.NewUnknown(complianceName, "No processed snapshot is available to read the firewall rules from", cx,
			[]string{"no processed snapshot; nothing was read"}, result.Options{NextActions: []string{"investigate-collection-failure"}})
	}
	sid := fwd.SnapshotID(cx)
	counts, _, ctrunc, err := s.RunNQEAll(ctx, in.NetworkID, sid, shadowCountsQuery, maxShadowRows)
	if err != nil {
		return result.Result{}, err
	}
	rows, total, truncated, err := s.RunNQEAll(ctx, in.NetworkID, sid, shadowRowsQuery, maxShadowRows)
	if err != nil {
		return result.Result{}, err
	}
	configured, zoned, devices := 0, 0, 0
	otherConfigured := 0
	otherOS := map[string]int{}
	for _, c := range counts {
		n1, _ := numOf(c["configured"])
		n2, _ := numOf(c["zoned"])
		os, _ := c["os"].(string)
		if permissiveVerifiedOS[os] {
			configured += int(n1)
			zoned += int(n2)
			if n1 > 0 {
				devices++
			}
		} else if n1 > 0 {
			otherConfigured += int(n1)
			otherOS[os] += int(n1)
		}
	}
	limits := []string{
		"the comparison uses the layer 3 and 4 match Forward models for an ACL entry (addresses, protocol, ports). A firewall rule can also require an application, user, URL category or schedule, which the ACL entries do not carry (device.securityPolicy has them but is EXPERIMENTAL, off by default, and is not read here): an 'any' rule with such a condition is narrower than it looks, so a finding is a candidate to confirm in the rule base, not proof",
		"'any' means an address set that covers the whole address space, or an entry with no address set at all (Forward models some any rules that way; the finding says which). Negated addresses, address objects Forward cannot resolve (dynamic groups, external lists, FQDNs) and services that use the application default are not visible in the entries, and they can make a rule LOOK like any (a false candidate) unless Forward marks the entry over-approximated: confirm in the rule base",
		"only zone-based PAN-OS rules are read; a rule with no zone scope, a default rule, a rule marked over-approximated and a rule that does not permit are not analyzed (counted in the evidence). Other vendors' rules are not read: the vendor must be one whose rule model was checked",
		"a rule with several service combinations is several entries with one name: it is reported once, at its worst entry, with how many of its entries are permissive",
		"a permissive rule can be shadowed by an earlier rule and so never decide anything: use view shadowed_rules for that; and whether a rule is used at all is a different question (the unused-rules query in reference/library-queries.md)",
	}
	if truncated || ctrunc {
		limits = append(limits, fmt.Sprintf("the read stopped at %d rows (%d exist): some rules were not read", maxShadowRows, total))
	}
	if !truncated && !ctrunc && len(rows) != zoned {
		limits = append(limits, fmt.Sprintf("the rows read (%d) differ from the zone-scoped rules counted (%d): the two reads saw different data", len(rows), zoned))
	}
	if len(otherOS) > 0 {
		names := make([]string, 0, len(otherOS))
		for os, n := range otherOS {
			names = append(names, fmt.Sprintf("%s %d", os, n))
		}
		sort.Strings(names)
		limits = append(limits, fmt.Sprintf("%d configured rule(s) on vendors not yet verified were not read (%s)", otherConfigured, strings.Join(names, ", ")))
	}
	if configured == 0 {
		return result.NewUnknown(complianceName, "No PAN-OS device has configured firewall rules in the model of this snapshot, so there is nothing to read", cx, limits,
			result.Options{NextActions: []string{"inspect-inventory", "plan-security-posture"}})
	}
	var skip shadowSkip
	var entries []*permissiveEntry
	permits := 0
	for _, r := range rows {
		if e, ok := classifyPermissive(r, &skip); ok {
			entries = append(entries, e)
			permits++
		}
	}
	findings := findPermissive(entries)
	scopeLine := fmt.Sprintf("%d PAN-OS rules on %d devices are configured; %d carry a zone scope; %d of those are permit entries that were analyzed", configured, devices, zoned, permits)
	tiers := map[string]int{}
	byDev := map[string]int{}
	byBasis := map[string]int{}
	for _, f := range findings {
		byBasis[f.Basis]++
		tiers[f.Tier]++
		byDev[f.Device]++
	}
	detail := map[string]any{"configured_rules": configured, "zone_scoped_rules": zoned, "rules_without_zone_scope": configured - zoned, "permit_entries_analyzed": permits,
		"skipped":        map[string]int{"default_rule": skip.Default, "over_approximated": skip.Approximated, "not_a_permit": skip.OtherAction, "half_an_address_family": skip.HalfFamily, "not_understood": skip.Unparseable},
		"by_basis":       byBasis,
		"findings_total": len(findings), "by_tier": tiers, "findings_by_device": byDev}
	if len(otherOS) > 0 {
		detail["vendors_not_read"] = otherOS
	}
	limit := in.Limit
	if limit <= 0 {
		limit = defaultPermissiveShown
	}
	shown, omitted := result.Cap(findings, limit, "permissive rule candidates", "give limit to see more")
	detail["findings"] = shown
	opts := result.Options{Limits: limits, Omitted: omitted, NextActions: []string{"inspect-device-files", "verify-change", "plan-security-posture"},
		Evidence: []result.Evidence{result.NewEvidence(result.EvPolicy, "permissiveRules", fwd.SnapshotIDPtr(snap), detail, scopeLine)}}
	if len(findings) == 0 {
		return result.NewUnknown(complianceName, fmt.Sprintf("No analyzed rule permits any source, any destination or any service: %s. That is not proof the rule base is tight (see the limits)", scopeLine), cx, limits, opts)
	}
	finding := fmt.Sprintf("%d rule(s) on %d device(s) are permissive candidates: %d any-source-any-destination-any-service, %d any-source-any-destination, %d any-service; %d of them rest on an entry with no address set (lower confidence); %s",
		len(findings), len(byDev), tiers[tierAnyAnyAny], tiers[tierAnyToAny], tiers[tierAnyService], byBasis[basisUnbounded], scopeLine)
	return result.Build(complianceName, result.Failed, finding, result.Inferred, cx, opts)
}
