package skills

import (
	"fmt"
	"sort"
	"testing"
)

// row builds one result row as the shadow query returns it: sets as lists of CIDR strings, ranges as {start,end}.
type srow struct {
	os                   string
	device, name, action string
	index                int
	siblings             int
	src4, dst4           []string
	src6, dst6           []string
	proto, tpSrc, tpDst  [][2]int
	icmpType, icmpCode   [][2]int
	ingress, egress      []string
	isDefault, approx    bool
}

func full(lo, hi int) [][2]int { return [][2]int{{lo, hi}} }

func (r srow) m() map[string]any {
	ranges := func(v [][2]int, def [2]int) []any {
		if v == nil {
			v = [][2]int{def}
		}
		var out []any
		for _, x := range v {
			out = append(out, map[string]any{"start": float64(x[0]), "end": float64(x[1])})
		}
		return out
	}
	strs := func(v []string) []any {
		out := []any{}
		for _, s := range v {
			out = append(out, s)
		}
		return out
	}
	os := r.os
	if os == "" {
		os = "PAN_OS"
	}
	ing, eg := r.ingress, r.egress
	if ing == nil {
		ing = []string{"z1"}
	}
	if eg == nil {
		eg = []string{"z2"}
	}
	return map[string]any{"os": os, "device": r.device, "index": float64(r.index), "siblings": float64(r.siblings), "name": r.name, "action": r.action, "isDefault": r.isDefault, "approximated": r.approx,
		"src4": strs(r.src4), "dst4": strs(r.dst4), "src6": strs(r.src6), "dst6": strs(r.dst6),
		"proto": ranges(r.proto, [2]int{0, 255}), "tpSrc": ranges(r.tpSrc, [2]int{0, 65535}), "tpDst": ranges(r.tpDst, [2]int{0, 65535}),
		"icmpType": ranges(r.icmpType, [2]int{0, 255}), "icmpCode": ranges(r.icmpCode, [2]int{0, 255}), "ingress": strs(ing), "egress": strs(eg)}
}

func analyze(rows ...srow) ([]shadowFinding, shadowSkip, int) {
	var skip shadowSkip
	var rules []*shadowRule
	for _, r := range rows {
		if rule, ok := parseShadowRule(r.m(), &skip); ok {
			rules = append(rules, rule)
		}
	}
	f, _, _ := findShadowed(rules)
	return f, skip, len(rules)
}

func analyzePartial(rows ...srow) ([]shadowFinding, int) {
	var skip shadowSkip
	var rules []*shadowRule
	for _, r := range rows {
		if rule, ok := parseShadowRule(r.m(), &skip); ok {
			rules = append(rules, rule)
		}
	}
	f, _, p := findShadowed(rules)
	return f, p
}

func any4(device, name, action string, i int) srow {
	return srow{device: device, name: name, action: action, index: i, src4: []string{"0.0.0.0/0"}, dst4: []string{"0.0.0.0/0"}}
}

func TestShadowRedundantAndIneffectiveByAction(t *testing.T) {
	a := srow{device: "fw", name: "broad", action: "PERMIT", index: 0, src4: []string{"10.0.0.0/8"}, dst4: []string{"0.0.0.0/0"}, proto: full(6, 6)}
	b := srow{device: "fw", name: "narrow", action: "PERMIT", index: 1, src4: []string{"10.1.0.0/16"}, dst4: []string{"192.168.1.0/24"}, proto: full(6, 6), tpDst: full(443, 443)}
	f, _, n := analyze(a, b)
	if n != 2 || len(f) != 1 || f[0].Rule != "narrow" || len(f[0].CoveredBy) != 1 || f[0].CoveredBy[0].Rule != "broad" || f[0].Kind != "redundant" {
		t.Fatalf("a narrower rule after a broader one with the same action is redundant: %+v", f)
	}
	b.action = "DENY"
	f, _, _ = analyze(a, b)
	if len(f) != 1 || f[0].Kind != "ineffective" || f[0].Action != "DENY" || f[0].CoveredBy[0].Action != "PERMIT" {
		t.Fatalf("the opposite action makes it ineffective: %+v", f)
	}
}

func TestShadowNotFoundWhenTheLaterRuleIsBroaderOrTheOrderIsReversed(t *testing.T) {
	broad := srow{device: "fw", name: "broad", action: "PERMIT", src4: []string{"10.0.0.0/8"}, dst4: []string{"0.0.0.0/0"}}
	narrow := srow{device: "fw", name: "narrow", action: "PERMIT", src4: []string{"10.1.0.0/16"}, dst4: []string{"192.168.1.0/24"}}
	narrow.index, broad.index = 0, 1 // the narrow rule comes first: it does not shadow the broad one
	if f, _, _ := analyze(narrow, broad); len(f) != 0 {
		t.Errorf("a narrower rule first covers nothing after it: %+v", f)
	}
	// the list index decides the order, not the order the rows arrive in
	broad.index, narrow.index = 0, 1
	if f, _, _ := analyze(narrow, broad); len(f) != 1 || f[0].Rule != "narrow" {
		t.Errorf("rows are compared in index order: %+v", f)
	}
}

func TestShadowUsesExactSetArithmeticForAdjacentPrefixesAndRanges(t *testing.T) {
	// two adjacent /25 prefixes in one earlier rule are the /24 below; two adjacent port ranges are 80-100
	i := srow{device: "fw", name: "two halves", action: "PERMIT", index: 0, src4: []string{"10.0.0.0/25", "10.0.0.128/25"}, dst4: []string{"0.0.0.0/0"}, tpDst: [][2]int{{80, 90}, {91, 100}}}
	j := srow{device: "fw", name: "whole", action: "PERMIT", index: 1, src4: []string{"10.0.0.0/24"}, dst4: []string{"1.2.3.0/24"}, tpDst: full(85, 95)}
	if f, _, _ := analyze(i, j); len(f) != 1 {
		t.Errorf("a /24 inside two adjacent /25s and a range inside two adjacent ranges is covered: %+v", f)
	}
	j.tpDst = full(85, 101) // one port past the earlier rule's ranges
	if f, _, _ := analyze(i, j); len(f) != 0 {
		t.Errorf("one port outside the earlier rule means not covered: %+v", f)
	}
	j.tpDst = full(85, 95)
	j.src4 = []string{"10.0.1.0/24"} // the next /24: outside
	if f, _, _ := analyze(i, j); len(f) != 0 {
		t.Errorf("an address outside the earlier rule means not covered: %+v", f)
	}
}

// Coverage by the union of several earlier rules is a known limit: it is not found, and the result says so.
func TestShadowDoesNotFindCoverageByTheUnionOfSeveralRules(t *testing.T) {
	a := srow{device: "fw", name: "low half", action: "PERMIT", index: 0, src4: []string{"10.0.0.0/9"}, dst4: []string{"0.0.0.0/0"}}
	b := srow{device: "fw", name: "high half", action: "PERMIT", index: 1, src4: []string{"10.128.0.0/9"}, dst4: []string{"0.0.0.0/0"}}
	c := srow{device: "fw", name: "whole", action: "PERMIT", index: 2, src4: []string{"10.0.0.0/8"}, dst4: []string{"0.0.0.0/0"}}
	if f, _, _ := analyze(a, b, c); len(f) != 0 {
		t.Errorf("the union of two earlier rules is a documented limit and is not reported: %+v", f)
	}
}

func TestShadowZonesAreAScopeAndAnInnerZoneIsCovered(t *testing.T) {
	i := any4("fw", "wide zones", "PERMIT", 0)
	i.ingress, i.egress = []string{"a", "b"}, []string{"x", "y"}
	j := any4("fw", "inside", "PERMIT", 1)
	j.ingress, j.egress = []string{"a"}, []string{"y"}
	if f, _, _ := analyze(i, j); len(f) != 1 {
		t.Errorf("zones inside the earlier rule's zones are covered: %+v", f)
	}
	j.ingress = []string{"c"} // a zone the earlier rule does not have
	if f, _, _ := analyze(i, j); len(f) != 0 {
		t.Errorf("a zone outside the earlier rule's means the later rule applies to traffic the earlier one does not: %+v", f)
	}
}

func TestShadowIPVersionsAreCoveredSeparately(t *testing.T) {
	v4only := srow{device: "fw", name: "v4", action: "PERMIT", index: 0, src4: []string{"0.0.0.0/0"}, dst4: []string{"0.0.0.0/0"}}
	both := srow{device: "fw", name: "both", action: "PERMIT", index: 1, src4: []string{"10.0.0.0/8"}, dst4: []string{"0.0.0.0/0"}, src6: []string{"2001:db8::/32"}, dst6: []string{"::/0"}}
	if f, _, _ := analyze(v4only, both); len(f) != 0 {
		t.Errorf("a rule that also matches IPv6 is not covered by an IPv4-only rule: %+v", f)
	}
	v6 := srow{device: "fw", name: "v6", action: "PERMIT", index: 0, src6: []string{"::/0"}, dst6: []string{"::/0"}}
	onlyv6 := srow{device: "fw", name: "narrow v6", action: "PERMIT", index: 1, src6: []string{"2001:db8:1::/48"}, dst6: []string{"2001:db8:2::/48"}}
	if f, _, _ := analyze(v6, onlyv6); len(f) != 1 {
		t.Errorf("an IPv6 rule inside an earlier IPv6 rule is covered: %+v", f)
	}
	if f, _, _ := analyze(v4only, onlyv6); len(f) != 0 {
		t.Errorf("an IPv6 rule is not covered by an IPv4 rule: %+v", f)
	}
}

// Entries the analysis cannot judge are counted by reason and never compared.
func TestShadowSkipsWhatItCannotJudgeAndCountsIt(t *testing.T) {
	good := any4("fw", "any", "PERMIT", 0)
	def := any4("fw", "default", "DENY", 1)
	def.isDefault = true
	appr := any4("fw", "approx", "PERMIT", 2)
	appr.approx = true
	pbr := any4("fw", "pbr", "PBR", 3)
	noIP := srow{device: "fw", name: "no ip version", action: "DENY", index: 4} // every address set empty
	half := srow{device: "fw", name: "half", action: "DENY", index: 5, src4: []string{"10.0.0.0/8"}}
	inner := srow{device: "fw", name: "inner", action: "PERMIT", index: 6, src4: []string{"10.0.0.0/8"}, dst4: []string{"10.0.0.0/8"}}
	f, skip, n := analyze(good, def, appr, pbr, noIP, half, inner)
	if skip.Default != 1 || skip.Approximated != 1 || skip.OtherAction != 1 || skip.NoIPVersion != 1 || skip.HalfFamily != 1 || skip.total() != 5 || n != 2 {
		t.Errorf("skipped %+v analyzed %d", skip, n)
	}
	if len(f) != 1 || f[0].Rule != "inner" {
		t.Errorf("the rules that can be judged are still compared: %+v", f)
	}
	// an entry that is over-approximated or an 'any' entry with no IP version must never be the covering rule
	wild := srow{device: "fw", name: "wild", action: "DENY", index: 0}
	later := any4("fw", "later", "PERMIT", 1)
	if f, _, _ := analyze(wild, later); len(f) != 0 {
		t.Errorf("an entry that does not say its IP version covers nothing: %+v", f)
	}
}

func TestShadowDevicesAreComparedSeparately(t *testing.T) {
	a := any4("fw1", "any", "PERMIT", 0)
	b := any4("fw2", "later", "PERMIT", 0)
	b.index = 1
	if f, _, _ := analyze(a, b); len(f) != 0 {
		t.Errorf("rules of two devices are never compared: %+v", f)
	}
}

func TestShadowSkipsADeviceWithTooManyRules(t *testing.T) {
	var rules []*shadowRule
	var skip shadowSkip
	for i := 0; i <= maxShadowRulesDevice; i++ {
		r := any4("big", fmt.Sprintf("r%d", i), "PERMIT", i)
		rule, _ := parseShadowRule(r.m(), &skip)
		rules = append(rules, rule)
	}
	f, tooLarge, _ := findShadowed(rules)
	names := []string{}
	for d := range tooLarge {
		names = append(names, d)
	}
	sort.Strings(names)
	if len(f) != 0 || len(names) != 1 || names[0] != "big" {
		t.Errorf("a device over the cap is not compared and is named: %d findings, %v", len(f), names)
	}
}

func TestPrefixIntervalsAreExact(t *testing.T) {
	cases := map[string][2]string{"0.0.0.0/0": {"0.0.0.0", "255.255.255.255"}, "10.0.0.0/8": {"10.0.0.0", "10.255.255.255"}, "192.168.1.5/32": {"192.168.1.5", "192.168.1.5"}}
	for p, want := range cases {
		ivs, ok := parsePrefixes([]any{p})
		if !ok || len(ivs) != 1 {
			t.Fatalf("%s: %v %v", p, ivs, ok)
		}
		lo, _ := parsePrefixes([]any{want[0] + "/32"})
		hi, _ := parsePrefixes([]any{want[1] + "/32"})
		if ivs[0].lo != lo[0].lo || ivs[0].hi != hi[0].hi {
			t.Errorf("%s is %v-%v, want %s-%s", p, ivs[0].lo, ivs[0].hi, want[0], want[1])
		}
	}
	if ivs, _ := parsePrefixes([]any{"::/0"}); ivs[0].lo != (u128{}) || ivs[0].hi != (u128{^uint64(0), ^uint64(0)}) {
		t.Errorf("::/0 covers the whole IPv6 space: %v", ivs)
	}
	if _, ok := parsePrefixes([]any{"not an address"}); ok {
		t.Errorf("garbage is not an address set")
	}
}

// Every field of the match is compared: a rule whose source is inside the earlier rule's but whose destination, zone, protocol, port or ICMP value is outside is not covered. One fixture per field,
// so removing any one comparison from covers makes a test fail.
func TestShadowEveryFieldOfTheMatchDecidesCoverage(t *testing.T) {
	base := func() (srow, srow) {
		i := srow{device: "fw", name: "i", action: "PERMIT", index: 0, src4: []string{"10.0.0.0/8"}, dst4: []string{"192.168.0.0/16"}, proto: full(6, 6), tpSrc: full(1024, 65535), tpDst: full(80, 90),
			icmpType: full(0, 20), icmpCode: full(0, 5), ingress: []string{"a", "b"}, egress: []string{"x", "y"}}
		j := srow{device: "fw", name: "j", action: "PERMIT", index: 1, src4: []string{"10.1.0.0/16"}, dst4: []string{"192.168.1.0/24"}, proto: full(6, 6), tpSrc: full(2000, 3000), tpDst: full(85, 85),
			icmpType: full(8, 8), icmpCode: full(0, 0), ingress: []string{"a"}, egress: []string{"x"}}
		return i, j
	}
	i, j := base()
	if f, _, _ := analyze(i, j); len(f) != 1 {
		t.Fatalf("the base pair is covered: %+v", f)
	}
	for name, mut := range map[string]func(j *srow){
		"source":       func(j *srow) { j.src4 = []string{"172.16.0.0/12"} },
		"destination":  func(j *srow) { j.dst4 = []string{"172.16.0.0/12"} },
		"ingress zone": func(j *srow) { j.ingress = []string{"c"} },
		"egress zone":  func(j *srow) { j.egress = []string{"z"} },
		"protocol":     func(j *srow) { j.proto = full(17, 17) },
		"source port":  func(j *srow) { j.tpSrc = full(80, 80) },
		"dest port":    func(j *srow) { j.tpDst = full(8080, 8080) },
		"icmp type":    func(j *srow) { j.icmpType = full(30, 30) },
		"icmp code":    func(j *srow) { j.icmpCode = full(9, 9) },
	} {
		i, j := base()
		mut(&j)
		if f, _, _ := analyze(i, j); len(f) != 0 {
			t.Errorf("a %s outside the earlier rule's must not be covered: %+v", name, f)
		}
	}
}

// A PAN-OS rule is universal, intrazone or interzone, and the model gives the same ingress and egress sets for all three, so the product of an earlier rule's sets is trusted only for the pairs
// every type matches.
func TestShadowZoneTypesOfTheEarlierRuleAreNotGuessed(t *testing.T) {
	zones := func(in, eg []string, jin, jeg []string) []shadowFinding {
		i := any4("fw", "earlier", "PERMIT", 0)
		i.ingress, i.egress = in, eg
		j := any4("fw", "later", "DENY", 1)
		j.ingress, j.egress = jin, jeg
		f, _, _ := analyze(i, j)
		return f
	}
	cases := []struct {
		name        string
		in, eg      []string
		jin, jeg    []string
		wantCovered bool
	}{
		{"disjoint zone sets: the three types agree", []string{"a"}, []string{"b"}, []string{"a"}, []string{"b"}, true},
		{"one zone in both: only (z,z), universal and intrazone agree", []string{"a"}, []string{"a"}, []string{"a"}, []string{"a"}, true},
		{"equal sets of two zones, later a to b: an intrazone rule does not match it", []string{"a", "b"}, []string{"a", "b"}, []string{"a"}, []string{"b"}, false},
		{"equal sets of two zones, later a to a: an interzone rule does not match it", []string{"a", "b"}, []string{"a", "b"}, []string{"a"}, []string{"a"}, false},
		{"overlapping sets, later pair of different zones: universal and interzone agree", []string{"a", "b"}, []string{"b", "c"}, []string{"a"}, []string{"c"}, true},
		{"overlapping sets, later pair with the same zone: an interzone rule does not match it", []string{"a", "b"}, []string{"b", "c"}, []string{"b"}, []string{"b"}, false},
		{"ingress strictly inside egress, later a to a: an interzone rule does not match it", []string{"a"}, []string{"a", "b"}, []string{"a"}, []string{"a"}, false},
		{"ingress strictly inside egress, later a to b: different zones, every type agrees", []string{"a"}, []string{"a", "b"}, []string{"a"}, []string{"b"}, true},
		{"egress strictly inside ingress, later b to b: an interzone rule does not match it", []string{"a", "b"}, []string{"b"}, []string{"b"}, []string{"b"}, false},
	}
	for _, c := range cases {
		if got := len(zones(c.in, c.eg, c.jin, c.jeg)) == 1; got != c.wantCovered {
			t.Errorf("%s: covered %v, want %v", c.name, got, c.wantCovered)
		}
	}
}

// Only vendors whose zone semantics and rule order were checked are compared; the others' entries are counted by operating system and never compared.
func TestShadowSkipsVendorsNotVerified(t *testing.T) {
	i := any4("fg", "any", "PERMIT", 0)
	i.os = "FORTIOS"
	j := any4("fg", "later", "PERMIT", 1)
	j.os = "FORTIOS"
	f, skip, n := analyze(i, j)
	if len(f) != 0 || n != 0 || skip.Vendor != 2 || skip.VendorOS["FORTIOS"] != 2 {
		t.Errorf("another vendor's entries are not analyzed: %+v skip %+v analyzed %d", f, skip, n)
	}
}

func TestShadowAnAddressThatDoesNotParseIsNotUnderstoodNotHalfAFamily(t *testing.T) {
	for _, bad := range []string{"fe80::1%eth0", "not-an-address", "10.0.0.0/33"} {
		r := any4("fw", "odd", "PERMIT", 0)
		r.src4 = []string{bad}
		_, skip, _ := analyze(r)
		if skip.Unparseable != 1 || skip.HalfFamily != 0 {
			t.Errorf("%q: %+v", bad, skip)
		}
	}
}

// Forward models a rule with several service combinations as several entries with one name. The rule is the unit: it is reported only when every entry is analyzed and covered; entries of one rule
// never cover each other.
func TestShadowReportsARuleOnlyWhenEveryEntryIsCovered(t *testing.T) {
	web := srow{device: "fw", name: "web", action: "PERMIT", index: 0, src4: []string{"10.0.0.0/8"}, dst4: []string{"0.0.0.0/0"}, proto: full(6, 6), tpDst: full(1, 1000)}
	dns := srow{device: "fw", name: "dns", action: "PERMIT", index: 1, src4: []string{"10.0.0.0/8"}, dst4: []string{"0.0.0.0/0"}, proto: full(17, 17), tpDst: full(1, 1000)}
	// one rule, two entries: tcp/80 and udp/53
	s1 := srow{device: "fw", name: "svc", action: "PERMIT", index: 2, siblings: 2, src4: []string{"10.1.0.0/16"}, dst4: []string{"1.1.1.0/24"}, proto: full(6, 6), tpDst: full(80, 80)}
	s2 := srow{device: "fw", name: "svc", action: "PERMIT", index: 3, siblings: 2, src4: []string{"10.1.0.0/16"}, dst4: []string{"1.1.1.0/24"}, proto: full(17, 17), tpDst: full(53, 53)}
	f, p := analyzePartial(web, dns, s1, s2)
	if len(f) != 1 || f[0].Rule != "svc" || f[0].Entries != 2 || len(f[0].CoveredBy) != 2 || f[0].Kind != "redundant" || p != 0 {
		t.Fatalf("a rule whose two entries are covered by two earlier rules is reported once, covered by both: %+v partial %d", f, p)
	}
	// the udp entry is not covered (the earlier rules do not carry udp/53 here): the rule is NOT shadowed and is counted as partly covered
	dns.proto = full(17, 17)
	dns.tpDst = full(1000, 2000)
	f, p = analyzePartial(web, dns, s1, s2)
	if len(f) != 0 || p != 1 {
		t.Fatalf("a rule with only one covered entry must not be reported (removing it would break the other traffic): %+v partial %d", f, p)
	}
}

func TestShadowARuleWithAnEntryThatCouldNotBeAnalyzedIsNotReported(t *testing.T) {
	wide := srow{device: "fw", name: "wide", action: "PERMIT", index: 0, src4: []string{"10.0.0.0/8"}, dst4: []string{"0.0.0.0/0"}}
	// the query counted three entries named svc, but only one of them was analyzable (the others had no zone scope, or were over-approximated)
	one := srow{device: "fw", name: "svc", action: "PERMIT", index: 1, siblings: 3, src4: []string{"10.1.0.0/16"}, dst4: []string{"1.1.1.0/24"}}
	f, p := analyzePartial(wide, one)
	if len(f) != 0 || p != 1 {
		t.Fatalf("a rule with entries that were not analyzed is not reported: %+v partial %d", f, p)
	}
	one.siblings = 1
	if f, _ := analyzePartial(wide, one); len(f) != 1 {
		t.Errorf("with every entry analyzed it is reported: %+v", f)
	}
}

func TestShadowEntriesOfOneRuleNeverCoverEachOther(t *testing.T) {
	a := srow{device: "fw", name: "same", action: "PERMIT", index: 0, siblings: 2, src4: []string{"10.0.0.0/8"}, dst4: []string{"0.0.0.0/0"}}
	b := srow{device: "fw", name: "same", action: "PERMIT", index: 1, siblings: 2, src4: []string{"10.1.0.0/16"}, dst4: []string{"1.1.1.0/24"}}
	if f, p := analyzePartial(a, b); len(f) != 0 || p != 0 {
		t.Errorf("one entry of a rule inside another entry of the same rule is not a shadow: %+v partial %d", f, p)
	}
	// two entries with no name stand alone
	a.name, b.name = "", ""
	a.siblings, b.siblings = 0, 0
	if f, _ := analyzePartial(a, b); len(f) != 1 {
		t.Errorf("unnamed entries are separate rules: %+v", f)
	}
}

func TestShadowKindIsRedundantIneffectiveOrMixedByTheCoveringRules(t *testing.T) {
	permit := srow{device: "fw", name: "permit web", action: "PERMIT", index: 0, src4: []string{"10.0.0.0/8"}, dst4: []string{"0.0.0.0/0"}, proto: full(6, 6)}
	deny := srow{device: "fw", name: "deny udp", action: "DENY", index: 1, src4: []string{"10.0.0.0/8"}, dst4: []string{"0.0.0.0/0"}, proto: full(17, 17)}
	m1 := srow{device: "fw", name: "both", action: "PERMIT", index: 2, siblings: 2, src4: []string{"10.1.0.0/16"}, dst4: []string{"1.1.1.0/24"}, proto: full(6, 6)}
	m2 := srow{device: "fw", name: "both", action: "PERMIT", index: 3, siblings: 2, src4: []string{"10.1.0.0/16"}, dst4: []string{"1.1.1.0/24"}, proto: full(17, 17)}
	f, _ := analyzePartial(permit, deny, m1, m2)
	if len(f) != 1 || f[0].Kind != "mixed" {
		t.Errorf("covered by a rule with the same action and one with the opposite action is mixed: %+v", f)
	}
}
