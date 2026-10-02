package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/forwardnetworks/fwdctl/nqelint"
	"github.com/forwardnetworks/fwdctl/skills"
)

// nqeSynthOpts are the flags of `fwdctl nqe synthesize internet`.
type nqeSynthOpts struct {
	network, vrf, device, discovery, subnets, snapshot string
	unlikely                                           bool
}

// nqeSynthesizeCmd derives a synthetic-device query from evidence in the network model. It runs the analysis inspect-edge runs, prints the query on stdout and lints it with the
// offline checks; output that is not clean is reported on stderr and exits non-zero.
func nqeSynthesizeCmd(a *app, o nqeSynthOpts) int {
	stdout, stderr, session := a.out, a.err, a.session
	network, vrf, device, discovery, subnets, snapshot, unlikely := &o.network, &o.vrf, &o.device, &o.discovery, &o.subnets, &o.snapshot, &o.unlikely
	if *network == "" {
		fmt.Fprintln(stderr, "error: --network is required")
		return usage
	}
	var subs []string
	for _, s := range strings.Split(*subnets, ",") {
		if s = strings.TrimSpace(s); s != "" {
			subs = append(subs, s)
		}
	}
	sess, err := session()
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return usage
	}
	// On a large network the route, interface-address and neighbor tables can each run past 100,000 rows; AnalyzeEdge reads them
	// concurrently, but the whole call can still take a while. A heartbeat on stderr says so, instead of leaving the caller
	// wondering whether the command is still working or stuck.
	done := make(chan struct{})
	defer close(done)
	go func() {
		t := time.NewTicker(20 * time.Second)
		defer t.Stop()
		start := time.Now()
		for {
			select {
			case <-done:
				return
			case <-t.C:
				fmt.Fprintf(stderr, "note: still reading the network model (%s elapsed); a large network's route and interface tables can run past 100,000 rows\n", time.Since(start).Round(time.Second))
			}
		}
	}()
	an, err := skills.AnalyzeEdge(context.Background(), sess, skills.EdgeQuery{NetworkID: *network, SnapshotID: *snapshot, VRF: *vrf, Device: *device})
	if err != nil {
		if errors.Is(err, skills.ErrNoProcessedSnapshot) {
			fmt.Fprintln(stderr, "error: the network has no processed snapshot; nothing was read")
			return 2
		}
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 3
	}
	out, err := skills.SynthesizeInternet(an, skills.SynthOptions{Discovery: *discovery, IncludeUnlikely: *unlikely, Subnets: subs, NetworkID: *network, VRF: *vrf, Device: *device})
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	diags := nqelint.Lint(out.Source)
	more, err := nqelint.CheckSyntheticRows(out.Source, "internet")
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	diags = append(diags, more...)
	if len(diags) > 0 {
		for _, d := range diags {
			fmt.Fprintf(stderr, "lint %s: line %d col %d: %s\n", d.Severity, d.Line, d.Column, d.Message)
		}
		fmt.Fprintln(stderr, "error: the generated query is not clean; it is not printed (a bug in the generator or an input it cannot express)")
		return 1
	}
	fmt.Fprint(stdout, out.Source)
	fmt.Fprintf(stderr, "note: %d connection row(s) from snapshot %s; lint and the internet row-type check are clean. Next: fwdctl nqe lint --synthetic internet, then validate-nqe-query with synthetic_kind internet.\n", len(out.Rows), an.SnapshotID)
	return 0
}
