package skills

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"sort"
	"strings"

	"github.com/forwardnetworks/fwdctl/fwd"
	"github.com/forwardnetworks/fwdctl/result"
)

const editInternetExclusionsName = "edit-internet-exclusions"

func init() { Register(editInternetExclusionsName, editInternetExclusions) }

type editInternetExclusionsInput struct {
	NetworkID string   `json:"network_id"`
	Add       []string `json:"add"`
	Remove    []string `json:"remove"`
	// Set replaces the whole list; a present empty list clears it (a nil/absent Set means "not asked").
	Set                *[]string `json:"set"`
	BackdateSnapshotID string    `json:"backdate_snapshot_id"`
	Apply              bool      `json:"apply"`
}

// forwardReservedBlocks are the ranges Forward's IpSubnetAddress.RESERVED_SUBNETS treats as not public (base/packet/IpSubnetAddress.java): multicast, future use 240/4, RFC 1918, 0/8,
// loopback, link-local, shared/CGNAT 100.64/10, IETF 192.0.0/24, the three documentation blocks, benchmarking 198.18/15, 6to4 relay 192.88.99/24, and for IPv6 multicast,
// link-local, unique local and the documentation block. Forward rejects an entry that lies ENTIRELY inside one of these; a wider block that mixes reserved and public space is accepted.
var forwardReservedBlocks = func() []netip.Prefix {
	var out []netip.Prefix
	for _, s := range []string{"224.0.0.0/4", "240.0.0.0/4", "10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "0.0.0.0/8", "127.0.0.0/8", "169.254.0.0/16", "100.64.0.0/10", "192.0.0.0/24",
		"192.0.2.0/24", "198.51.100.0/24", "203.0.113.0/24", "198.18.0.0/15", "192.88.99.0/24", "ff00::/8", "fe80::/10", "fc00::/7", "2001:db8::/32"} {
		out = append(out, netip.MustParsePrefix(s))
	}
	return out
}()

// reservedBlocks are the ranges inspect-edge treats as not public when it asks whether a next hop is a public address: broader than Forward's rule for excluded subnets (see
// forwardReservedBlocks), and it leaves the documentation blocks alone so that synthetic fixtures can use them.
var reservedBlocks = func() []netip.Prefix {
	var out []netip.Prefix
	for _, s := range []string{"0.0.0.0/8", "10.0.0.0/8", "100.64.0.0/10", "127.0.0.0/8", "169.254.0.0/16", "172.16.0.0/12", "192.0.0.0/24", "192.168.0.0/16", "224.0.0.0/3", "::/8", "fc00::/7", "fe80::/10", "ff00::/8"} {
		out = append(out, netip.MustParsePrefix(s))
	}
	return out
}()

// normalizeExclusion checks one entry the way Forward does on input: a CIDR block with the host bits zero (a bare address is a /32 or /128), in
// public space. Forward re-checks on the server; failing here names the entry before anything is sent.
func normalizeExclusion(s string) (string, error) {
	s = strings.TrimSpace(s)
	if !strings.Contains(s, "/") {
		a, err := netip.ParseAddr(s)
		if err != nil {
			return "", fmt.Errorf("%q is not an IP address or CIDR block", s)
		}
		s = netip.PrefixFrom(a, a.BitLen()).String()
	}
	p, err := netip.ParsePrefix(s)
	if err != nil {
		return "", fmt.Errorf("%q is not a CIDR block", s)
	}
	if p.Masked() != p {
		return "", fmt.Errorf("%q has host bits set (did you mean %s?)", s, p.Masked())
	}
	for _, r := range forwardReservedBlocks {
		if r.Bits() <= p.Bits() && r.Contains(p.Addr()) {
			return "", fmt.Errorf("%q lies inside %s, which is private or reserved space", s, r)
		}
	}
	return p.String(), nil
}

// normalizeExclusions normalizes every entry and refuses the whole list naming EVERY rejected entry (up to 20, then a count), so one dry run shows all of them.
func normalizeExclusions(in []string) ([]string, error) {
	seen := map[string]bool{}
	out := []string{}
	var bad []string
	for _, s := range in {
		n, err := normalizeExclusion(s)
		if err != nil {
			bad = append(bad, err.Error())
			continue
		}
		if !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	if len(bad) > 0 {
		shown := bad[:min(len(bad), 20)]
		msg := fmt.Sprintf("%d of %d entries are not acceptable: %s", len(bad), len(in), strings.Join(shown, "; "))
		if len(bad) > len(shown) {
			msg += fmt.Sprintf("; and %d more", len(bad)-len(shown))
		}
		if len(in) > 100 {
			msg += ". A long list usually comes from a routing-table dump, which can hold private, shared, benchmarking (198.18.0.0/15), documentation or multicast ranges: filter those out first"
		}
		return nil, errors.New(msg)
	}
	sort.Strings(out)
	return out, nil
}

func sameList(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// editInternetExclusions changes the internet node's excluded subnets: public prefixes that are routed inside the network and must not be located at the
// internet node. Forward stores one list and a write replaces all of it, so the skill reads the current list, applies add, remove or set to it, shows
// before and after, writes the whole result and reads it back. The prior list is the undo.
func editInternetExclusions(ctx context.Context, s *fwd.Session, raw json.RawMessage) (result.Result, error) {
	var in editInternetExclusionsInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return result.Result{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	if in.NetworkID == "" {
		return result.Result{}, fmt.Errorf("%w: network_id is required", ErrInvalidInput)
	}
	asked := 0
	for _, b := range []bool{len(in.Add) > 0, len(in.Remove) > 0, in.Set != nil} {
		if b {
			asked++
		}
	}
	if (in.Set != nil && (len(in.Add) > 0 || len(in.Remove) > 0)) || asked == 0 {
		return result.Result{}, fmt.Errorf("%w: give add and/or remove (a change to the current list) or set (the whole list, [] clears it)", ErrInvalidInput)
	}
	var add, remove, set []string
	var err error
	if add, err = normalizeExclusions(in.Add); err != nil {
		return result.Result{}, fmt.Errorf("%w: add: %v", ErrInvalidInput, err)
	}
	if remove, err = normalizeExclusions(in.Remove); err != nil {
		return result.Result{}, fmt.Errorf("%w: remove: %v", ErrInvalidInput, err)
	}
	if in.Set != nil {
		if set, err = normalizeExclusions(*in.Set); err != nil {
			return result.Result{}, fmt.Errorf("%w: set: %v", ErrInvalidInput, err)
		}
	}
	mode := result.ModeDryRun
	if in.Apply {
		mode = result.ModeApplied
	}
	cx := result.Context{NetworkID: in.NetworkID, State: "current"}
	next := []string{"inspect-topology", "investigate-reachability"}
	var plan *backdatePlan
	if in.BackdateSnapshotID != "" {
		if plan, err = planBackdate(ctx, s, in.NetworkID, in.BackdateSnapshotID); err != nil {
			return result.Result{}, err
		}
		if plan == nil {
			return result.NewUnknown(editInternetExclusionsName, fmt.Sprintf("The network holds no snapshot %s to backdate to", in.BackdateSnapshotID), cx,
				[]string{"no such snapshot (ids are matched exactly); nothing was changed"}, result.Options{NextActions: []string{"inspect-snapshots"}})
		}
	}
	node, err := s.InternetNode(ctx, in.NetworkID)
	if err != nil {
		return result.Result{}, err
	}
	if node == nil {
		return result.NewUnknown(editInternetExclusionsName, "The network has no internet node to read the exclusions from", cx,
			[]string{"the internet node could not be read (none is modelled, or this Forward does not serve it); nothing was changed"}, result.Options{NextActions: []string{"inspect-topology"}})
	}
	before, err := normalizeExclusions(node.SubnetsToExclude)
	if err != nil {
		before = append([]string{}, node.SubnetsToExclude...)
		sort.Strings(before)
	}
	var after []string
	if in.Set != nil {
		after = set
	} else {
		drop := map[string]bool{}
		for _, r := range remove {
			drop[r] = true
		}
		after = []string{}
		for _, b := range before {
			if !drop[b] {
				after = append(after, b)
			}
		}
		after = append(after, add...)
		if after, err = normalizeExclusions(after); err != nil {
			return result.Result{}, err
		}
	}
	limits := []string{"excluded subnets are one list on the internet node and a write replaces all of it; they apply from the next processed snapshot (or, with backdate_snapshot_id, an existing one now); this skill cannot show how paths change, trace the flow before and after",
		"exclusions are prefix-based: there is no per-interface or per-device exclusion, and an address a collected interface carries is located there before the internet node is considered, so excluding it does not hide it from an internet-addressable result; read plan-synthetic-device reference/exposure.md",
		"an exclusion keeps a public prefix off the internet node (it is located by its internal route instead); it does not make the prefix reachable"}
	if plan != nil {
		limits = append(limits, plan.Note)
	}
	if len(node.Connections)+nodeQueryCount(node) == 0 {
		limits = append(limits, "the internet node has no connection, so it is inert and owns nothing yet: the list is stored and takes effect once it has one")
	}
	ev := func(extra map[string]any) []result.Evidence {
		d := map[string]any{"before": before, "after": after, "added": minus(after, before), "removed": minus(before, after), "mode": mode}
		if plan != nil {
			d["backdate"] = plan
		}
		for k, v := range extra {
			d[k] = v
		}
		return []result.Evidence{result.NewEvidence(result.EvTopology, "internetExcludedSubnets", nil, d, "")}
	}
	if sameList(before, after) {
		return result.Build(editInternetExclusionsName, result.OK, fmt.Sprintf("The internet node already excludes exactly those %d subnet(s); nothing to change", len(after)), result.Deterministic, cx,
			result.Options{Mode: mode, Evidence: ev(nil), Limits: limits, NextActions: next})
	}
	beforeJ, _ := json.Marshal(before)
	afterJ, _ := json.Marshal(after)
	undo := "run edit-internet-exclusions with set = this change's before (the whole prior list) and apply=true"
	if plan != nil {
		undo += "; the backdate itself cannot be undone (the snapshots it invalidated are reprocessed, recomputing the same data)"
	}
	ch := result.Change{Action: "set_internet_excluded_subnets", Target: "internet node excluded subnets", Before: string(beforeJ), After: string(afterJ), Reversible: true, Undo: undo}
	summary := fmt.Sprintf("%d subnet(s) now, %d after (+%d, -%d)", len(before), len(after), len(minus(after, before)), len(minus(before, after)))
	if len(after) == 0 {
		limits = append(limits, "this CLEARS the whole list: every prefix it kept off the internet node will be claimed by it again")
	}
	if !in.Apply {
		extra := ""
		if plan != nil {
			extra = fmt.Sprintf(", then backdate to snapshot %s (invalidating %d snapshot(s) so they reprocess now)", plan.Snapshot, len(plan.Affected))
		}
		return result.Build(editInternetExclusionsName, result.OK, fmt.Sprintf("Dry run: would replace the internet node's excluded subnets (%s)%s. Nothing was changed; run again with apply=true to make it", summary, extra),
			result.Deterministic, cx, result.Options{Mode: result.ModeDryRun, Changes: []result.Change{ch}, Evidence: ev(nil), Limits: limits, NextActions: next})
	}
	if _, err := s.SetInternetExcludedSubnets(ctx, in.NetworkID, after); err != nil {
		return result.Result{}, fmt.Errorf("the change failed, nothing is known to have changed: %w", err)
	}
	ch.Applied = true
	now, err := s.InternetNode(ctx, in.NetworkID)
	if err != nil || now == nil {
		return result.Result{}, fmt.Errorf("the change was sent but reading the internet node back failed, so it is not proven: %v", err)
	}
	held, _ := normalizeExclusions(now.SubnetsToExclude)
	if !sameList(held, after) {
		return result.Build(editInternetExclusionsName, result.Failed, "Forward accepted the change but the internet node's excluded subnets are not the requested list", result.Deterministic, cx,
			result.Options{Mode: result.ModeApplied, Changes: []result.Change{ch}, Evidence: ev(map[string]any{"held": false, "now": held}), Limits: limits})
	}
	finding := fmt.Sprintf("Applied: the internet node's excluded subnets are now %d (%s)", len(after), summary)
	if plan != nil {
		if err := s.BackdateSynthetic(ctx, in.NetworkID, "internet", in.BackdateSnapshotID); err != nil {
			return result.Build(editInternetExclusionsName, result.Failed, fmt.Sprintf("The exclusions were applied but the backdate to snapshot %s failed: %v", in.BackdateSnapshotID, err), result.Deterministic, cx,
				result.Options{Mode: result.ModeApplied, Changes: []result.Change{ch}, Evidence: ev(map[string]any{"held": true}), Limits: append(limits, "the change applies from the next snapshot; retry the backdate or wait for one"), NextActions: []string{"inspect-snapshots"}})
		}
		finding += fmt.Sprintf("; backdated to snapshot %s (%d snapshot(s) now UNPROCESSED; Forward does not reprocess them by itself: run edit-snapshot-reprocess for each one, then edit-advanced-reachability if internet exposure is needed, since that only runs after a snapshot is PROCESSED)", in.BackdateSnapshotID, len(plan.Affected))
		next = []string{"edit-snapshot-reprocess", "edit-advanced-reachability", "inspect-snapshots", "inspect-topology", "investigate-reachability"}
	}
	return result.Build(editInternetExclusionsName, result.OK, finding, result.Deterministic, cx,
		result.Options{Mode: result.ModeApplied, Changes: []result.Change{ch}, Evidence: ev(map[string]any{"held": true}), Limits: limits, NextActions: next})
}

func minus(a, b []string) []string {
	in := map[string]bool{}
	for _, x := range b {
		in[x] = true
	}
	out := []string{}
	for _, x := range a {
		if !in[x] {
			out = append(out, x)
		}
	}
	return out
}
