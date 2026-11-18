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

// check-network-compliance view shadowed_rules: which zone-based firewall rules are covered by an earlier single rule, read from the ACL entries Forward models for each device.
//
// What Forward's model says (read from AclNatFetcherUtils on release/26.11.0-10 and from live results): a device's aclEntries is an ORDERED list; an entry's headerMatches holds the layer 3 and 4
// match (address sets per IP version, protocol and port ranges, ICMP type and code), where an unconstrained field is the full range and an address set is EMPTY for an IP version the entry does
// not match; metadataMatches holds the ingress and egress interfaces, filled from the rule's zones and EMPTY for an ACL that is applied separately to interfaces (several such ACLs sit in one flat
// list, so their entries cannot be compared); overApproximated says a field is wider than the rule. Nothing in the model records a rule's application, user, URL category or schedule conditions.

// shadowRowsQuery returns each configured, zone-scoped entry of a device of a verified vendor with its position in the device's list and the number of entries that share its name (Forward
// models a rule with several service combinations as several entries with one name). The position is explicit (the order of a result is not guaranteed); an entry without a zone scope is left out
// here and counted by shadowCountsQuery.
var shadowRowsQuery = buildShadowRowsQuery()

func buildShadowRowsQuery() string {
	var oses []string
	for os := range shadowVerifiedOS {
		oses = append(oses, "device.platform.os == OS."+os)
	}
	sort.Strings(oses)
	return `foreach device in network.devices
where ` + strings.Join(oses, " || ") + `
let entries = device.aclEntries
where length(entries) > 0
foreach i in fromTo(0, length(entries) - 1)
let e = entries[i]
where !e.implicitRule
where length(e.metadataMatches.ingressInterfaces) > 0
where length(e.metadataMatches.egressInterfaces) > 0
select {
  device: device.name,
  os: device.platform.os,
  index: i,
  siblings: length(foreach x in entries where x.name == e.name select x),
  name: e.name,
  action: when e.action is
            DENY -> "DENY";
            PBR -> "PBR";
            PERMIT -> "PERMIT";
            EVALUATE -> "EVALUATE",
  isDefault: e.defaultRule,
  approximated: e.headerMatches.overApproximated,
  src4: e.headerMatches.ipv4SrcSet,
  dst4: e.headerMatches.ipv4DstSet,
  src6: e.headerMatches.ipv6SrcSet,
  dst6: e.headerMatches.ipv6DstSet,
  proto: e.headerMatches.ipProtocol,
  tpSrc: e.headerMatches.tpSrc,
  tpDst: e.headerMatches.tpDst,
  icmpType: e.headerMatches.icmpType,
  icmpCode: e.headerMatches.icmpCode,
  ingress: e.metadataMatches.ingressInterfaces,
  egress: e.metadataMatches.egressInterfaces
}`
}

// shadowCountsQuery counts, per device with ACL entries, the configured entries and how many carry a zone scope.
const shadowCountsQuery = `foreach device in network.devices
where length(device.aclEntries) > 0
select {
  device: device.name,
  os: device.platform.os,
  configured: length(foreach e in device.aclEntries where !e.implicitRule select e),
  zoned: length(foreach e in device.aclEntries
                where !e.implicitRule
                where length(e.metadataMatches.ingressInterfaces) > 0
                where length(e.metadataMatches.egressInterfaces) > 0
                select e)
}`

// shadowVerifiedOS are the vendors whose zone semantics and first-match rule order were checked for this analysis (PAN-OS: measured on a live network and read from Forward's model). The view
// compares entries of other vendors' devices only after the same is done for them: FortiOS may be ordered by policy id, an SRX evaluates global policies after zone-pair policies, and a Check Point
// layer does not end evaluation at a permit, so a position in the list is not first-match order there.
var shadowVerifiedOS = map[string]bool{"PAN_OS": true}

const (
	maxShadowRows        = 200000 // rows read in one run
	maxShadowRulesDevice = 3000   // rules compared per device (pairs grow with the square)
	defaultShadowShown   = 25
)

// ---- exact interval arithmetic: an address set and a range list are unions of intervals, and "covered" is containment of every interval of one in the union of the other ----

type u128 struct{ hi, lo uint64 }

func (a u128) less(b u128) bool { return a.hi < b.hi || (a.hi == b.hi && a.lo < b.lo) }

func (a u128) plusOne() (u128, bool) { // false on overflow
	if a.lo == ^uint64(0) {
		if a.hi == ^uint64(0) {
			return u128{}, false
		}
		return u128{a.hi + 1, 0}, true
	}
	return u128{a.hi, a.lo + 1}, true
}

type ival struct{ lo, hi u128 }

func prefixInterval(p netip.Prefix) ival {
	p = p.Masked()
	a := p.Addr()
	b := a.As16()
	lo := u128{beUint64(b[0:8]), beUint64(b[8:16])}
	bits := p.Bits()
	if a.Is4() {
		bits += 96 // an IPv4 address is held as ::ffff:a.b.c.d; its prefix is 96 bits longer
	}
	hi := lo
	for k := bits; k < 128; k++ { // set the host bits
		if k < 64 {
			hi.hi |= 1 << (63 - uint(k))
		} else {
			hi.lo |= 1 << (127 - uint(k))
		}
	}
	return ival{lo, hi}
}

func beUint64(b []byte) uint64 {
	var v uint64
	for _, x := range b {
		v = v<<8 | uint64(x)
	}
	return v
}

// mergeIvals sorts and merges overlapping or adjacent intervals into a disjoint, ordered union.
func mergeIvals(in []ival) []ival {
	if len(in) == 0 {
		return nil
	}
	v := append([]ival(nil), in...)
	sort.Slice(v, func(i, j int) bool { return v[i].lo.less(v[j].lo) })
	out := []ival{v[0]}
	for _, x := range v[1:] {
		last := &out[len(out)-1]
		next, ok := last.hi.plusOne()
		if !ok || !next.less(x.lo) { // x starts at or before last.hi+1
			if last.hi.less(x.hi) {
				last.hi = x.hi
			}
			continue
		}
		out = append(out, x)
	}
	return out
}

// ivalsWithin says whether every interval of sub lies inside one interval of the merged union sup.
func ivalsWithin(sub, sup []ival) bool {
	for _, s := range sub {
		ok := false
		for _, u := range sup {
			if !s.lo.less(u.lo) && !u.hi.less(s.hi) {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	return true
}

type numRange struct{ lo, hi uint64 }

func mergeRngs(in []numRange) []numRange {
	if len(in) == 0 {
		return nil
	}
	v := append([]numRange(nil), in...)
	sort.Slice(v, func(i, j int) bool { return v[i].lo < v[j].lo })
	out := []numRange{v[0]}
	for _, x := range v[1:] {
		last := &out[len(out)-1]
		if x.lo <= last.hi+1 {
			if x.hi > last.hi {
				last.hi = x.hi
			}
			continue
		}
		out = append(out, x)
	}
	return out
}

func rngsWithin(sub, sup []numRange) bool {
	for _, s := range sub {
		ok := false
		for _, u := range sup {
			if s.lo >= u.lo && s.hi <= u.hi {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	return true
}

// ---- the rules ----

// family is a rule's match for one IP version: the product of a source set and a destination set. A rule has the family only when both sets are non-empty.
type family struct{ src, dst []ival }

type shadowRule struct {
	device         string
	index          int
	siblings       int // entries of the device that share this entry's name, as the query counted them (0: not given)
	name           string
	action         string
	v4, v6         *family
	proto, tpSrc   []numRange
	tpDst          []numRange
	icmpType       []numRange
	icmpCode       []numRange
	ingress, egres map[string]bool
}

// shadowSkip counts why an entry was not analyzed.
type shadowSkip struct {
	Default, Approximated, OtherAction, NoIPVersion, HalfFamily, Unparseable, Vendor int
	VendorOS                                                                         map[string]int // the operating systems skipped for the vendor reason, and how many entries each
}

func (k shadowSkip) total() int {
	return k.Default + k.Approximated + k.OtherAction + k.NoIPVersion + k.HalfFamily + k.Unparseable + k.Vendor
}

func asList(v any) ([]any, bool) {
	if v == nil {
		return nil, true
	}
	l, ok := v.([]any)
	return l, ok
}

func parsePrefixes(v any) ([]ival, bool) {
	l, ok := asList(v)
	if !ok {
		return nil, false
	}
	var out []ival
	for _, x := range l {
		s, ok := x.(string)
		if !ok {
			return nil, false
		}
		p, err := netip.ParsePrefix(strings.TrimSpace(s))
		if err != nil {
			a, aerr := netip.ParseAddr(strings.TrimSpace(s)) // a single address
			if aerr != nil || a.Zone() != "" {               // a zone identifier (fe80::1%eth0) is not part of a match set
				return nil, false
			}
			p = netip.PrefixFrom(a, a.BitLen())
		}
		out = append(out, prefixInterval(p))
	}
	return mergeIvals(out), true
}

func numOf(v any) (uint64, bool) {
	switch n := v.(type) {
	case float64:
		if n < 0 {
			return 0, false
		}
		return uint64(n), true
	case int:
		if n < 0 {
			return 0, false
		}
		return uint64(n), true
	case int64:
		if n < 0 {
			return 0, false
		}
		return uint64(n), true
	}
	return 0, false
}

func parseRanges(v any) ([]numRange, bool) {
	l, ok := asList(v)
	if !ok || len(l) == 0 { // a range field is never empty on a real rule (unconstrained is the full range): an empty one is not understood
		return nil, false
	}
	var out []numRange
	for _, x := range l {
		m, ok := x.(map[string]any)
		if !ok {
			return nil, false
		}
		lo, ok1 := numOf(m["start"])
		hi, ok2 := numOf(m["end"])
		if !ok1 || !ok2 || hi < lo {
			return nil, false
		}
		out = append(out, numRange{lo, hi})
	}
	return mergeRngs(out), true
}

func parseNames(v any) (map[string]bool, bool) {
	l, ok := asList(v)
	if !ok || len(l) == 0 {
		return nil, false
	}
	out := map[string]bool{}
	for _, x := range l {
		s, ok := x.(string)
		if !ok {
			return nil, false
		}
		out[s] = true
	}
	return out, true
}

// parseFamily returns the family, whether the rule has it, and an error kind: "" (understood), "half" (only one of the two sets is set) or "bad" (an address that does not parse).
func parseFamily(src, dst any) (*family, bool, string) {
	s, ok1 := parsePrefixes(src)
	d, ok2 := parsePrefixes(dst)
	if !ok1 || !ok2 {
		return nil, false, "bad"
	}
	switch {
	case len(s) == 0 && len(d) == 0:
		return nil, false, ""
	case len(s) == 0 || len(d) == 0:
		return nil, false, "half"
	}
	return &family{s, d}, true, ""
}

// parseShadowRule turns one result row into a rule, or says why it is not analyzed.
func parseShadowRule(r map[string]any, skip *shadowSkip) (*shadowRule, bool) {
	rule := &shadowRule{}
	if os, _ := r["os"].(string); !shadowVerifiedOS[os] {
		skip.Vendor++
		if skip.VendorOS == nil {
			skip.VendorOS = map[string]int{}
		}
		skip.VendorOS[os]++
		return nil, false
	}
	rule.device, _ = r["device"].(string)
	rule.name, _ = r["name"].(string)
	rule.action, _ = r["action"].(string)
	if n, ok := numOf(r["index"]); ok {
		rule.index = int(n)
	} else {
		skip.Unparseable++
		return nil, false
	}
	if n, ok := numOf(r["siblings"]); ok {
		rule.siblings = int(n)
	}
	if d, _ := r["isDefault"].(bool); d {
		skip.Default++
		return nil, false
	}
	if a, _ := r["approximated"].(bool); a {
		skip.Approximated++
		return nil, false
	}
	if rule.action != "PERMIT" && rule.action != "DENY" {
		skip.OtherAction++
		return nil, false
	}
	var ok bool
	var has4, has6 bool
	var bad4, bad6 string
	rule.v4, has4, bad4 = parseFamily(r["src4"], r["dst4"])
	rule.v6, has6, bad6 = parseFamily(r["src6"], r["dst6"])
	if bad4 == "bad" || bad6 == "bad" {
		skip.Unparseable++
		return nil, false
	}
	if bad4 != "" || bad6 != "" {
		skip.HalfFamily++
		return nil, false
	}
	if !has4 && !has6 {
		skip.NoIPVersion++ // every address set empty: the entry does not say which IP version it matches, so its addresses are not known
		return nil, false
	}
	if rule.proto, ok = parseRanges(r["proto"]); !ok {
		skip.Unparseable++
		return nil, false
	}
	if rule.tpSrc, ok = parseRanges(r["tpSrc"]); !ok {
		skip.Unparseable++
		return nil, false
	}
	if rule.tpDst, ok = parseRanges(r["tpDst"]); !ok {
		skip.Unparseable++
		return nil, false
	}
	if rule.icmpType, ok = parseRanges(r["icmpType"]); !ok {
		skip.Unparseable++
		return nil, false
	}
	if rule.icmpCode, ok = parseRanges(r["icmpCode"]); !ok {
		skip.Unparseable++
		return nil, false
	}
	if rule.ingress, ok = parseNames(r["ingress"]); !ok {
		skip.Unparseable++
		return nil, false
	}
	if rule.egres, ok = parseNames(r["egress"]); !ok {
		skip.Unparseable++
		return nil, false
	}
	return rule, true
}

func namesWithin(sub, sup map[string]bool) bool {
	for k := range sub {
		if !sup[k] {
			return false
		}
	}
	return true
}

// zonesSound says whether the earlier rule's zone product can be trusted to cover the later rule's pairs. A PAN-OS rule has a type: universal (every source and destination zone pair),
// intrazone (only pairs with the same zone) or interzone (only pairs with different zones), and the model gives the same ingress and egress sets for all three, so the product of the sets
// can claim pairs the rule does not match. A pair is safe to claim only when every type matches it:
//   - the sets share no zone: no same-zone pair exists, the three types agree;
//   - the sets are equal and hold one zone z: the only pair is (z,z), which universal and intrazone match (interzone is not possible);
//   - the sets are equal with several zones: no pair is matched by all three types, so the rule shadows nothing;
//   - the sets overlap but differ: intrazone is not possible, and universal and interzone agree on every pair of different zones, so the later rule must have no same-zone pair.
func zonesSound(i, j *shadowRule) bool {
	shared := false
	for z := range i.ingress {
		if i.egres[z] {
			shared = true
			break
		}
	}
	if !shared {
		return true
	}
	if namesWithin(i.ingress, i.egres) && namesWithin(i.egres, i.ingress) { // equal sets
		return len(i.ingress) == 1
	}
	for z := range j.ingress {
		if j.egres[z] {
			return false
		}
	}
	return true
}

func familyWithin(sub, sup *family) bool {
	if sub == nil { // the rule has no packets of this IP version: nothing to cover
		return true
	}
	return sup != nil && ivalsWithin(sub.src, sup.src) && ivalsWithin(sub.dst, sup.dst)
}

// covers says whether every packet the later rule j matches is also matched by the earlier rule i: the same zones or fewer, and per IP version a source and destination set inside i's, and
// every protocol, port and ICMP range inside i's. It compares two rules only: j covered by the union of several earlier rules is not found.
func covers(i, j *shadowRule) bool {
	return zonesSound(i, j) && namesWithin(j.ingress, i.ingress) && namesWithin(j.egres, i.egres) &&
		familyWithin(j.v4, i.v4) && familyWithin(j.v6, i.v6) &&
		rngsWithin(j.proto, i.proto) && rngsWithin(j.tpSrc, i.tpSrc) && rngsWithin(j.tpDst, i.tpDst) &&
		rngsWithin(j.icmpType, i.icmpType) && rngsWithin(j.icmpCode, i.icmpCode)
}

type shadowCover struct {
	Rule   string `json:"rule"`
	Index  int    `json:"index"`
	Action string `json:"action"`
}

type shadowFinding struct {
	Device    string        `json:"device"`
	Rule      string        `json:"rule"`
	Index     int           `json:"index"` // the position of the rule's first entry
	Entries   int           `json:"entries"`
	Action    string        `json:"action"`
	CoveredBy []shadowCover `json:"covered_by"`
	// Kind: redundant (every covering rule has the same action), ineffective (every covering rule has the opposite action, so this rule never decides a packet) or mixed (both).
	Kind string `json:"kind"`
}

// ruleKey is the identity of a rule on a device: its name. An entry with no name stands alone.
func ruleKey(r *shadowRule) string {
	if r.name == "" {
		return fmt.Sprintf("#%d", r.index)
	}
	return r.name
}

// findShadowed compares, per device, each entry with the entries before it in the device's list and finds the first earlier entry of ANOTHER rule that covers it. Forward models a rule with several
// service combinations as several entries of one name, so the unit reported is the rule: a rule is reported only when every one of its entries was analyzed (the query counted as many same-name
// entries as were analyzed) and every one is covered. A rule with some entries covered is counted in partial, not reported. A device with more than maxShadowRulesDevice analyzable entries is not
// compared (it is returned in tooLarge).
func findShadowed(rules []*shadowRule) (findings []shadowFinding, tooLarge map[string]int, partial int) {
	by := map[string][]*shadowRule{}
	for _, r := range rules {
		by[r.device] = append(by[r.device], r)
	}
	devs := make([]string, 0, len(by))
	for d := range by {
		devs = append(devs, d)
	}
	sort.Strings(devs)
	tooLarge = map[string]int{}
	for _, d := range devs {
		rs := by[d]
		sort.SliceStable(rs, func(a, b int) bool { return rs[a].index < rs[b].index })
		if len(rs) > maxShadowRulesDevice {
			tooLarge[d] = len(rs)
			continue
		}
		cover := make([]*shadowRule, len(rs)) // the first earlier entry of another rule that covers entry j
		for j := range rs {
			for i := 0; i < j; i++ {
				if rs[i].index != rs[j].index && ruleKey(rs[i]) != ruleKey(rs[j]) && covers(rs[i], rs[j]) {
					cover[j] = rs[i]
					break
				}
			}
		}
		var order []string
		group := map[string][]int{}
		for j, r := range rs {
			k := ruleKey(r)
			if _, seen := group[k]; !seen {
				order = append(order, k)
			}
			group[k] = append(group[k], j)
		}
		for _, k := range order {
			idx := group[k]
			covered := 0
			sameAction := true
			for _, j := range idx {
				if cover[j] != nil {
					covered++
				}
				if rs[j].action != rs[idx[0]].action {
					sameAction = false
				}
			}
			if covered == 0 {
				continue
			}
			first := rs[idx[0]]
			complete := covered == len(idx) && sameAction && (first.siblings == 0 || first.siblings == len(idx))
			if !complete {
				partial++
				continue
			}
			var by []shadowCover
			seen := map[string]bool{}
			same, opposite := 0, 0
			for _, j := range idx {
				c := cover[j]
				if c.action == first.action {
					same++
				} else {
					opposite++
				}
				if !seen[ruleKey(c)] {
					seen[ruleKey(c)] = true
					by = append(by, shadowCover{Rule: c.name, Index: c.index, Action: c.action})
				}
			}
			kind := "mixed"
			switch {
			case opposite == 0:
				kind = "redundant"
			case same == 0:
				kind = "ineffective"
			}
			findings = append(findings, shadowFinding{Device: d, Rule: first.name, Index: first.index, Entries: len(idx), Action: first.action, CoveredBy: by, Kind: kind})
		}
	}
	return findings, tooLarge, partial
}

// shadowedRules is view shadowed_rules.
func shadowedRules(ctx context.Context, s *fwd.Session, in complianceInput) (result.Result, error) {
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
	// per-vendor totals from the counts query: the verified vendors' rules are the ones compared; the others' are counted and named
	configured, zoned, devices := 0, 0, 0
	otherConfigured, otherDevices := 0, 0
	otherOS := map[string]int{}
	for _, c := range counts {
		n1, _ := numOf(c["configured"])
		n2, _ := numOf(c["zoned"])
		os, _ := c["os"].(string)
		if shadowVerifiedOS[os] {
			configured += int(n1)
			zoned += int(n2)
			if n1 > 0 {
				devices++
			}
			continue
		}
		if n1 > 0 {
			otherConfigured += int(n1)
			otherDevices++
			otherOS[os] += int(n1)
		}
	}
	limits := []string{
		"only a rule covered by ONE earlier rule is found: a rule covered by the union of several earlier rules is not, so a clean result does not prove no rule is shadowed",
		"Forward models a rule with several service combinations as several entries that share one name, so a rule is reported only when EVERY one of its entries was analyzed and is covered by earlier entries of other rules; a rule with only some entries covered is counted, not reported",
		"the comparison uses the layer 3 and 4 match Forward models for an ACL entry (zones, addresses, protocol, ports, ICMP). A firewall rule can also require an application, user, URL category or schedule, which the ACL entries do not carry (device.securityPolicy has them but is EXPERIMENTAL, off by default, and is not read here): an earlier rule with such a condition is narrower than it looks, so a finding is a candidate to confirm in the rule base, not proof",
		"only zone-based rules are compared (the entry has ingress and egress interfaces). A rule with no zone scope, or whose entry does not say which IP version it matches (every address set empty), is not analyzed: an 'any' rule is typically one of these, and so is an ACL applied separately to interfaces (several of those share one list and cannot be told apart). So an any-to-any rule above others is not detected",
		"a rule marked over-approximated, a default rule and a rule whose action is not permit or deny are not analyzed",
		"only vendors whose zone semantics and first-match order were checked are compared (PAN-OS today); the entries of other vendors' devices are counted and skipped, because a position in the list is not first-match order everywhere (FortiOS may be ordered by policy id, an SRX evaluates global policies after zone-pair policies, a Check Point layer does not end at a permit)",
		"a PAN-OS rule is universal, intrazone or interzone and the model gives the same zone sets for all three, so an earlier rule whose zone sets overlap is trusted only for the pairs every type matches (an earlier rule over several interfaces with equal ingress and egress sets shadows nothing here). The built-in intrazone default is modelled with equal ingress and egress sets, one entry per zone; how a configured intrazone or interzone rule is modelled was not measured, because the measured network has none",
		"not visible in the ACL entries, so unchecked: whether a disabled rule is listed (a disabled earlier rule would not shadow anything), the order of Panorama pre, local and post rules, negated addresses and services that use the application default, and addresses Forward cannot resolve (dynamic address groups, external lists, FQDN objects): those can leave an earlier rule modelled WIDER than configured (a false finding) or a later rule modelled narrower (it can look covered when it is not) unless Forward marks the entry over-approximated",
		"order is the position of the entry in the device's list as Forward models it; hit counts, which show whether a rule is used, are a separate question (the unused-rules query in reference/library-queries.md)",
	}
	if truncated || ctrunc {
		limits = append(limits, fmt.Sprintf("the read stopped at %d rows (%d exist): some rules were not compared", maxShadowRows, total))
	}
	if !truncated && !ctrunc && len(rows) != zoned {
		limits = append(limits, fmt.Sprintf("the rows read (%d) differ from the zone-scoped rules counted (%d): the two reads saw different data, so some rules may be missing from the comparison", len(rows), zoned))
	}
	if len(otherOS) > 0 {
		names := make([]string, 0, len(otherOS))
		for os := range otherOS {
			names = append(names, fmt.Sprintf("%s %d", os, otherOS[os]))
		}
		sort.Strings(names)
		limits = append(limits, fmt.Sprintf("%d configured rule(s) on %d device(s) of vendors not yet verified for this analysis were not compared (%s)", otherConfigured, otherDevices, strings.Join(names, ", ")))
	}
	if configured == 0 {
		return result.NewUnknown(complianceName, "No PAN-OS device has configured firewall rules in the model of this snapshot, so there is nothing to compare", cx, limits,
			result.Options{NextActions: []string{"inspect-inventory"}})
	}
	var skip shadowSkip
	var rules []*shadowRule
	for _, r := range rows {
		if rule, ok := parseShadowRule(r, &skip); ok {
			rules = append(rules, rule)
		}
	}
	findings, tooLarge, partial := findShadowed(rules)
	scopeLine := fmt.Sprintf("%d PAN-OS rules on %d devices are configured; %d carry a zone scope; %d of those were analyzed", configured, devices, zoned, len(rules))
	if otherConfigured > 0 {
		scopeLine += fmt.Sprintf(" (%d rules on other vendors' devices were not compared)", otherConfigured)
	}
	noScope := configured - zoned
	detail := map[string]any{"configured_rules": configured, "devices_with_rules": devices, "zone_scoped_rules": zoned, "rules_without_zone_scope": noScope, "analyzed_rules": len(rules),
		"rules_partly_covered": partial,
		"skipped": map[string]int{"default_rule": skip.Default, "over_approximated": skip.Approximated, "action_not_permit_or_deny": skip.OtherAction, "no_ip_version": skip.NoIPVersion,
			"half_an_address_family": skip.HalfFamily, "not_understood": skip.Unparseable}}
	if len(otherOS) > 0 {
		detail["vendors_not_compared"] = otherOS
	}
	if len(tooLarge) > 0 {
		names := make([]string, 0, len(tooLarge))
		for d := range tooLarge {
			names = append(names, d)
		}
		sort.Strings(names)
		detail["devices_not_compared"] = names
		limits = append(limits, fmt.Sprintf("%d device(s) have more than %d analyzable rules and were not compared: %s", len(names), maxShadowRulesDevice, strings.Join(names, ", ")))
	}
	red, ineff, mixed := 0, 0, 0
	byDev := map[string]int{}
	for _, f := range findings {
		byDev[f.Device]++
		switch f.Kind {
		case "redundant":
			red++
		case "ineffective":
			ineff++
		default:
			mixed++
		}
	}
	detail["findings_total"], detail["redundant"], detail["ineffective"], detail["mixed"], detail["findings_by_device"] = len(findings), red, ineff, mixed, byDev
	limit := in.Limit
	if limit <= 0 {
		limit = defaultShadowShown
	}
	shown, omitted := result.Cap(findings, limit, "shadowed rule candidates", "give limit to see more")
	detail["findings"] = shown
	opts := result.Options{Limits: limits, Omitted: omitted, NextActions: []string{"inspect-device-files", "verify-change"},
		Evidence: []result.Evidence{result.NewEvidence(result.EvPolicy, "shadowedRules", fwd.SnapshotIDPtr(snap), detail, scopeLine)}}
	if len(findings) == 0 {
		return result.NewUnknown(complianceName, fmt.Sprintf("No rule is covered by earlier rules: %s. That is not proof that no rule is shadowed (see the limits)", scopeLine), cx, limits, opts)
	}
	finding := fmt.Sprintf("%d rule(s) on %d device(s) are covered by earlier rules and are candidates to remove or reorder: %d redundant (the same action), %d ineffective (the earlier rule decides the opposite way first) and %d mixed; %s",
		len(findings), len(byDev), red, ineff, mixed, scopeLine)
	return result.Build(complianceName, result.Failed, finding, result.Inferred, cx, opts)
}
