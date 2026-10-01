package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/forwardnetworks/fwdctl/fwd"
	"github.com/forwardnetworks/fwdctl/nqelint"
	"github.com/forwardnetworks/fwdctl/skills"
)

// nqeSynthesizeCmd derives a synthetic-device query from evidence in the network model: fwdctl nqe synthesize internet --network ID [--vrf V] [--device D]
// [--discovery interfaceAddresses|bgpRoutes|ipRoutes|none] [--subnets CIDR,...] [--include-unlikely] [--snapshot ID]. It runs the analysis inspect-edge runs, prints the query on
// stdout and lints it with the offline checks; output that is not clean is reported on stderr and exits non-zero.
func nqeSynthesizeCmd(args []string, stdout, stderr io.Writer, session func() (*fwd.Session, error)) int {
	const use = "usage: fwdctl nqe synthesize internet --network ID [--vrf V] [--device D] [--discovery interfaceAddresses|bgpRoutes|ipRoutes|none] [--subnets CIDR,...] [--include-unlikely] [--snapshot ID]"
	if len(args) == 0 || args[0] != "internet" {
		fmt.Fprintln(stderr, use)
		return usage
	}
	fs := flag.NewFlagSet("nqe synthesize internet", flag.ContinueOnError)
	fs.SetOutput(stderr)
	network := fs.String("network", "", "network id (required)")
	vrf := fs.String("vrf", "", "only default routes in this VRF")
	device := fs.String("device", "", "only default routes on this device")
	discovery := fs.String("discovery", "interfaceAddresses", "interfaceAddresses, bgpRoutes, ipRoutes or none")
	subnets := fs.String("subnets", "", "comma-separated prefixes written on every row (required for --discovery none)")
	unlikely := fs.Bool("include-unlikely", false, "also write rows for unowned exits that are not likely internet edges")
	snapshot := fs.String("snapshot", "", "snapshot id (default: the latest processed)")
	if err := fs.Parse(args[1:]); err != nil || fs.NArg() > 0 || *network == "" {
		fmt.Fprintln(stderr, use)
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
