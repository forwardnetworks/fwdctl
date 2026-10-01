package skills

import (
	"fmt"
	"sort"
	"strings"
)

// Confidence in a likely internet edge. likely_internet_edge only says: an unowned public next hop with an eBGP session to an unmodelled
// peer. On a network whose internal WAN runs on public address space that is also true of a carrier or partner WAN, so each likely edge
// carries a confidence (high, medium, low) built from evidence a reader can check: the peer AS class, whether the peer sends only a
// default (received count of 1), and what the VRF is called. It is a ranking aid, never a proof.

// asClass says what kind of AS number a peer uses: "private" (16-bit 64512-65534 or 32-bit 4200000000-4294967294), "own" (the session is
// iBGP or the AS is the AS of another modelled device) or "public".
func asClass(peerAS, localAS int64, modelledAS bool) string {
	switch {
	case peerAS == localAS || modelledAS:
		return "own"
	case (peerAS >= 64512 && peerAS <= 65534) || (peerAS >= 4200000000 && peerAS <= 4294967294):
		return "private"
	}
	return "public"
}

// vrfHint classifies a VRF name: "internet" (inet, internet), "edge" (dmz, public, transit) or "wan" (an internal WAN more often than
// an internet exit). "" when the name says nothing. The match is on the name as a whole word or a word of a dash/underscore/dot name,
// or as a prefix or suffix of it, so "amer" and "internal" match nothing.
func vrfHint(name string) string {
	n := strings.ToLower(name)
	words := strings.FieldsFunc(n, func(r rune) bool { return r == '-' || r == '_' || r == '.' || r == ' ' || r == '/' })
	has := func(w string) bool {
		for _, x := range words {
			if x == w {
				return true
			}
		}
		return false
	}
	switch {
	case has("inet") || has("internet") || strings.HasPrefix(n, "internet") || strings.HasSuffix(n, "internet") || strings.HasPrefix(n, "inet") && len(n) <= 6:
		return "internet"
	case has("dmz") || has("public") || has("transit") || strings.HasSuffix(n, "dmz"):
		return "edge"
	case has("wan") || strings.HasSuffix(n, "wan"):
		return "wan"
	}
	return ""
}

var confRank = map[string]int{"high": 0, "medium": 1, "low": 2, "": 3}

var vrfHintRank = map[string]int{"internet": 0, "edge": 1, "": 2, "wan": 3}

// edgeAssessment is the confidence of one likely edge and why.
type edgeAssessment struct {
	Confidence    string
	PeerAS        int64
	PeerASClass   string
	ReceivedKnown bool
	DefaultOnly   bool
	VRFHint       string
	Reasons       []string
}

// Assess scores a likely internet edge from its qualifying peers (eBGP, unmodelled, not an AS of a modelled device). The best peer
// decides. Rules: a private peer AS with no received count is LOW whatever the VRF is called (the name is a hint, not evidence);
// otherwise a public peer AS, a peer that sends only a default and an internet-named VRF each add a point: 2 or more is high, 1 medium.
func (g *edgeGroup) Assess() *edgeAssessment {
	if !g.Likely() {
		return nil
	}
	var best *edgeAssessment
	for _, k := range keysAny(g.bgp) {
		b := g.bgp[k]
		if b["ebgp"] != true || b["peer_as_is_a_modelled_device_as"] != false {
			continue
		}
		as, _ := num(b["peer_as"])
		la, _ := num(b["local_as"])
		a := &edgeAssessment{PeerAS: as, PeerASClass: asClass(as, la, false), VRFHint: vrfHint(g.VRF)}
		pts := 0
		if rv, ok := num(b["received_prefixes"]); ok {
			a.ReceivedKnown = true
			if rv == 1 {
				a.DefaultOnly = true
				pts++
				a.Reasons = append(a.Reasons, "the peer sent 1 prefix, consistent with a default only")
			} else {
				a.Reasons = append(a.Reasons, fmt.Sprintf("the peer sent %d prefixes, not a default only", rv))
			}
		} else {
			a.Reasons = append(a.Reasons, "received count is null: the default-only signal is unavailable")
		}
		switch a.PeerASClass {
		case "public":
			pts++
			a.Reasons = append(a.Reasons, "peer AS is public")
		case "private":
			a.Reasons = append(a.Reasons, "peer AS is private (64512-65534 or 4200000000-4294967294): typical of an internal or carrier WAN, rarely of an internet upstream")
		}
		if a.VRFHint == "internet" {
			pts++
			a.Reasons = append(a.Reasons, "VRF name says internet")
		} else if a.VRFHint != "" {
			a.Reasons = append(a.Reasons, "VRF name hints "+a.VRFHint)
		}
		switch {
		case a.PeerASClass == "private" && !a.ReceivedKnown:
			a.Confidence = "low"
		case pts >= 2:
			a.Confidence = "high"
		case pts == 1:
			a.Confidence = "medium"
		default:
			a.Confidence = "low"
		}
		if best == nil || confRank[a.Confidence] < confRank[best.Confidence] || confRank[a.Confidence] == confRank[best.Confidence] && a.PeerAS < best.PeerAS {
			best = a
		}
	}
	return best
}

func (a *edgeAssessment) Map() map[string]any {
	return map[string]any{"confidence": a.Confidence, "peer_as": a.PeerAS, "peer_as_class": a.PeerASClass, "received_known": a.ReceivedKnown,
		"default_only": a.DefaultOnly, "vrf_hint": nilIfEmpty(a.VRFHint), "confidence_reasons": a.Reasons}
}

// likelyEdgeGroups folds every likely edge into one row per (VRF, best peer AS), most confident first, so 113 rows read as about ten
// lines. It also returns counts by confidence and how many likely edges have a null received count.
func likelyEdgeGroups(exits []*edgeGroup) (rows []map[string]any, byConf map[string]int, nullReceived, likely int) {
	type key struct {
		vrf string
		as  int64
	}
	type acc struct {
		a       *edgeAssessment
		vrf     string
		edges   int
		devices map[string]bool
		nulls   int
	}
	m := map[key]*acc{}
	byConf = map[string]int{}
	for _, g := range exits {
		a := g.Assess()
		if a == nil {
			continue
		}
		likely++
		byConf[a.Confidence]++
		if !a.ReceivedKnown {
			nullReceived++
		}
		k := key{g.VRF, a.PeerAS}
		c := m[k]
		if c == nil {
			c = &acc{a: a, vrf: g.VRF, devices: map[string]bool{}}
			m[k] = c
		}
		if confRank[a.Confidence] < confRank[c.a.Confidence] {
			c.a = a
		}
		c.edges++
		c.nulls += map[bool]int{true: 0, false: 1}[a.ReceivedKnown]
		for d := range g.devices {
			c.devices[d] = true
		}
	}
	accs := make([]*acc, 0, len(m))
	for _, c := range m {
		accs = append(accs, c)
	}
	sort.Slice(accs, func(i, j int) bool {
		x, y := accs[i], accs[j]
		if confRank[x.a.Confidence] != confRank[y.a.Confidence] {
			return confRank[x.a.Confidence] < confRank[y.a.Confidence]
		}
		if vrfHintRank[x.a.VRFHint] != vrfHintRank[y.a.VRFHint] {
			return vrfHintRank[x.a.VRFHint] < vrfHintRank[y.a.VRFHint]
		}
		if x.edges != y.edges {
			return x.edges > y.edges
		}
		if x.vrf != y.vrf {
			return x.vrf < y.vrf
		}
		return x.a.PeerAS < y.a.PeerAS
	})
	for _, c := range accs {
		row := c.a.Map()
		row["vrf"], row["likely_edges"], row["devices"] = c.vrf, c.edges, len(c.devices)
		row["received_null_on"] = c.nulls
		rows = append(rows, row)
	}
	return rows, byConf, nullReceived, likely
}
