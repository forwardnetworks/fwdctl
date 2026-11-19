package skills

import "testing"

func permissive(rows ...srow) ([]permissiveFinding, shadowSkip) {
	var skip shadowSkip
	var es []*permissiveEntry
	for _, r := range rows {
		if e, ok := classifyPermissive(r.m(), &skip); ok {
			es = append(es, e)
		}
	}
	return findPermissive(es), skip
}

var zero4 = []string{"0.0.0.0/0"}
var zero6 = []string{"::/0"}

func tiers(f []permissiveFinding) map[string]string {
	m := map[string]string{}
	for _, x := range f {
		m[x.Rule] = x.Tier
	}
	return m
}

func TestPermissiveTiersFollowAnySourceAnyDestinationAndAnyService(t *testing.T) {
	cases := []struct {
		name string
		r    srow
		want string
	}{
		{"all", srow{src4: zero4, dst4: zero4}, tierAnyAnyAny},
		{"to-any-specific-service", srow{src4: zero4, dst4: zero4, proto: full(6, 6), tpDst: full(443, 443)}, tierAnyToAny},
		{"any-service-from-any", srow{src4: zero4, dst4: []string{"10.0.0.0/8"}}, tierAnyService},
		{"any-service-to-any", srow{src4: []string{"10.0.0.0/8"}, dst4: zero4}, tierAnyService},
		{"specific-both-any-service", srow{src4: []string{"10.0.0.0/8"}, dst4: []string{"192.0.2.0/24"}}, ""},
		{"any-source-specific-service", srow{src4: zero4, dst4: []string{"10.0.0.0/8"}, proto: full(6, 6), tpDst: full(443, 443)}, ""},
		{"v6-any-any-any", srow{src6: zero6, dst6: zero6}, tierAnyAnyAny},
		{"halves-merge-to-any", srow{src4: []string{"0.0.0.0/1", "128.0.0.0/1"}, dst4: zero4}, tierAnyAnyAny},
		{"almost-any", srow{src4: []string{"0.0.0.0/1"}, dst4: zero4}, tierAnyService},
		{"any-source-v4-any-dest-v6-is-half-a-family-and-skipped", srow{src4: zero4, dst6: zero6}, ""},
	}
	for _, c := range cases {
		c.r.device, c.r.name, c.r.action = "fw", c.name, "PERMIT"
		f, _ := permissive(c.r)
		got := tiers(f)[c.name]
		if got != c.want {
			t.Errorf("%s: tier %q, want %q", c.name, got, c.want)
		}
	}
}

func TestPermissiveEveryServiceFieldMustBeFullForAnyService(t *testing.T) {
	base := srow{device: "fw", name: "r", action: "PERMIT", src4: zero4, dst4: []string{"10.0.0.0/8"}}
	for _, mod := range []struct {
		what string
		set  func(*srow)
	}{
		{"protocol", func(r *srow) { r.proto = full(6, 6) }},
		{"source port", func(r *srow) { r.tpSrc = full(1024, 65535) }},
		{"destination port", func(r *srow) { r.tpDst = full(0, 65534) }},
	} {
		r := base
		mod.set(&r)
		if f, _ := permissive(r); len(f) != 0 {
			t.Errorf("a limited %s is not any service: %v", mod.what, f)
		}
	}
	// ICMP fields do not decide whether a rule is any service
	r := base
	r.icmpType = full(8, 8)
	if f, _ := permissive(r); len(f) != 1 {
		t.Errorf("icmp type is not part of any service: %v", f)
	}
}

func TestPermissiveAnEntryWithNoAddressSetIsUnconstrainedAndSaysSo(t *testing.T) {
	f, _ := permissive(srow{device: "fw", name: "any-to-any", action: "PERMIT"})
	if len(f) != 1 || f[0].Tier != tierAnyAnyAny || f[0].Basis != basisUnbounded {
		t.Fatalf("%+v", f)
	}
	f, _ = permissive(srow{device: "fw", name: "r", action: "PERMIT", src4: zero4, dst4: zero4})
	if len(f) != 1 || f[0].Basis != basisAddressSet {
		t.Errorf("an address set that covers everything has the other basis: %+v", f)
	}
}

func TestPermissiveSkipsWhatItCannotJudgeAndCountsIt(t *testing.T) {
	f, skip := permissive(
		srow{device: "fw", name: "deny", action: "DENY", src4: zero4, dst4: zero4},
		srow{device: "fw", name: "default", action: "PERMIT", isDefault: true, src4: zero4, dst4: zero4},
		srow{device: "fw", name: "approx", action: "PERMIT", approx: true, src4: zero4, dst4: zero4},
		srow{os: "FORTIOS", device: "fg", name: "other", action: "PERMIT", src4: zero4, dst4: zero4},
		srow{device: "fw", name: "bad", action: "PERMIT", src4: []string{"not-an-address"}, dst4: zero4},
	)
	if len(f) != 0 || skip.OtherAction != 1 || skip.Default != 1 || skip.Approximated != 1 || skip.Vendor != 1 || skip.Unparseable != 1 {
		t.Errorf("findings %v skip %+v", f, skip)
	}
}

func TestPermissiveReportsARuleOnceAtItsWorstEntry(t *testing.T) {
	f, _ := permissive(
		srow{device: "fw", name: "web", action: "PERMIT", index: 4, siblings: 3, src4: zero4, dst4: []string{"10.0.0.0/8"}},
		srow{device: "fw", name: "web", action: "PERMIT", index: 5, siblings: 3, src4: zero4, dst4: zero4},
		srow{device: "fw", name: "web", action: "PERMIT", index: 6, siblings: 3, src4: []string{"10.0.0.0/8"}, dst4: []string{"10.1.0.0/16"}, proto: full(6, 6)},
	)
	if len(f) != 1 || f[0].Tier != tierAnyAnyAny || f[0].PermissiveEntries != 2 || f[0].Entries != 3 || f[0].Index != 4 {
		t.Fatalf("one rule, worst tier, 2 of 3 entries permissive, first permissive index: %+v", f)
	}
}

func TestPermissiveOrdersWorstTierFirstThenDeviceAndPosition(t *testing.T) {
	f, _ := permissive(
		srow{device: "b", name: "svc", action: "PERMIT", index: 1, src4: zero4, dst4: []string{"10.0.0.0/8"}},
		srow{device: "b", name: "all2", action: "PERMIT", index: 9, src4: zero4, dst4: zero4},
		srow{device: "a", name: "all1", action: "PERMIT", index: 3, src4: zero4, dst4: zero4},
		srow{device: "a", name: "toany", action: "PERMIT", index: 2, src4: zero4, dst4: zero4, proto: full(6, 6), tpDst: full(80, 80)},
	)
	var order []string
	for _, x := range f {
		order = append(order, x.Rule)
	}
	if len(order) != 4 || order[0] != "all1" || order[1] != "all2" || order[2] != "toany" || order[3] != "svc" {
		t.Errorf("order %v", order)
	}
}

func TestPermissiveUnnamedEntriesStandAlone(t *testing.T) {
	f, _ := permissive(
		srow{device: "fw", action: "PERMIT", index: 1, src4: zero4, dst4: zero4},
		srow{device: "fw", action: "PERMIT", index: 2, src4: zero4, dst4: zero4},
	)
	if len(f) != 2 {
		t.Errorf("two unnamed entries are two rules: %v", f)
	}
}

// An entry with addresses on only one side of an IP version matches nothing in that version: it is counted, not read as 'any'.
func TestPermissiveHalfAnAddressFamilyIsSkippedAndCounted(t *testing.T) {
	f, skip := permissive(
		srow{device: "fw", name: "half-src", action: "PERMIT", src4: zero4},
		srow{device: "fw", name: "half-dst", action: "PERMIT", dst6: zero6},
	)
	if len(f) != 0 || skip.HalfFamily != 2 {
		t.Errorf("findings %v skip %+v", f, skip)
	}
}

// Result rows are not guaranteed to arrive in order: the reported index is the smallest, the tier the worst, and a tie sorts by device and position.
func TestPermissiveDoesNotDependOnTheOrderRowsArriveIn(t *testing.T) {
	f, _ := permissive(
		srow{device: "fw", name: "web", action: "PERMIT", index: 9, siblings: 3, src4: zero4, dst4: []string{"10.0.0.0/8"}},
		srow{device: "fw", name: "web", action: "PERMIT", index: 2, siblings: 3},
		srow{device: "fw", name: "web", action: "PERMIT", index: 5, siblings: 3, src4: zero4, dst4: zero4, proto: full(6, 6)},
	)
	if len(f) != 1 || f[0].Index != 2 || f[0].Tier != tierAnyAnyAny || f[0].Basis != basisUnbounded || f[0].PermissiveEntries != 3 || f[0].Entries != 3 {
		t.Fatalf("%+v", f)
	}
	// the worse tier arrives first, with a different basis: the worse one stays
	f, _ = permissive(
		srow{device: "fw", name: "r", action: "PERMIT", index: 1, src4: zero4, dst4: zero4},
		srow{device: "fw", name: "r", action: "PERMIT", index: 2},
	)
	if len(f) != 1 || f[0].Basis != basisAddressSet || f[0].PermissiveEntries != 2 {
		t.Errorf("an equal tier keeps the first basis: %+v", f)
	}
	g, _ := permissive(
		srow{device: "b", name: "x", action: "PERMIT", index: 3, src4: zero4, dst4: zero4},
		srow{device: "a", name: "y", action: "PERMIT", index: 8, src4: zero4, dst4: zero4},
		srow{device: "a", name: "z", action: "PERMIT", index: 4, src4: zero4, dst4: zero4},
	)
	if len(g) != 3 || g[0].Rule != "z" || g[1].Rule != "y" || g[2].Rule != "x" {
		t.Errorf("same tier: device, then position: %+v", g)
	}
}

func TestPermissiveEntriesIsReportedOnlyForANamedRuleTheQueryCounted(t *testing.T) {
	f, _ := permissive(srow{device: "fw", name: "n", action: "PERMIT", siblings: 4, src4: zero4, dst4: zero4}, srow{device: "fw", action: "PERMIT", index: 1, siblings: 9, src4: zero4, dst4: zero4})
	if len(f) != 2 {
		t.Fatalf("%+v", f)
	}
	for _, x := range f {
		if x.Rule == "n" && x.Entries != 4 {
			t.Errorf("a named rule reports the count: %+v", x)
		}
		if x.Rule == "" && x.Entries != 0 {
			t.Errorf("an unnamed entry reports no count: %+v", x)
		}
	}
	if g, _ := permissive(srow{device: "fw", name: "m", action: "PERMIT", src4: zero4, dst4: zero4}); len(g) != 1 || g[0].Entries != 0 {
		t.Errorf("a count the query did not give is not invented: %+v", g)
	}
}
