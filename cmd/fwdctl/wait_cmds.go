package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"

	forward "github.com/forwardnetworks/forward-go-sdk"
	"github.com/spf13/cobra"
)

// Exit codes of `fwdctl wait snapshot`: the wait ended because the state was reached, because a state can no longer be reached (failed, canceled, timed out on Forward's side),
// or because --timeout passed first. They are the result statuses' codes (ok 0, failed 1, unknown 2).
const (
	waitReached  = 0
	waitFailed   = 1
	waitTimedOut = 2
)

func (a *app) waitCmd() *cobra.Command {
	c := parent(&cobra.Command{
		Use: "wait", GroupID: "skills", Short: "block until Forward finishes something (a snapshot's processing, its advanced reachability)",
		Long: "Blocks, printing one progress line per poll on stderr, until Forward reaches the state asked for. For the work that takes an hour (a reprocess after a backdate, advanced\n" +
			"reachability) so it does not need a polling loop that dies with the session. Exit 0: reached. 1: Forward ended in a failed, canceled or timed-out state that will not change\n" +
			"by itself. 2: --timeout passed first (the work may still be running; run the wait again). 3: an error (bad input, Forward unreachable for several polls in a row).",
	})
	c.AddCommand(a.waitSnapshotCmd())
	return c
}

func (a *app) waitSnapshotCmd() *cobra.Command {
	var network, snapshot string
	var advanced bool
	var timeout, interval time.Duration
	c := &cobra.Command{
		Use: "snapshot --network ID --snapshot ID [--advanced-reachability] [--timeout 2h]", Short: "wait for a snapshot to be processed, and optionally its advanced reachability",
		Long: "Polls the snapshot every --interval until it is PROCESSED and, with --advanced-reachability, its advanced reachability is PROCESSED too.\n" +
			"A FAILED, CANCELED or TIMED_OUT state ends the wait with exit 1: Forward does not retry those by itself (a reprocess clears them). The last line on stdout is a JSON object: status,\n" +
			"snapshot_id, state, advanced_reachability, waited_seconds. Typical durations on a 1,400-device network: processing about an hour, advanced reachability 15 to 30 minutes.\n" +
			"Nothing here starts the work: a reprocess or advanced reachability that was never started (edit-snapshot, edit-advanced-reachability) stays UNPROCESSED, and the wait says so after a few polls and ends at --timeout.",
		Example: "  fwdctl wait snapshot --network N --snapshot S --advanced-reachability --timeout 3h",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if network == "" || snapshot == "" || timeout <= 0 || interval < time.Second {
				fmt.Fprintln(a.err, "error: usage: fwdctl wait snapshot --network ID --snapshot ID [--state PROCESSED] [--advanced-reachability PROCESSED] [--timeout 2h] [--interval 30s]")
				return a.exit(usage)
			}
			want, wantAdv := "PROCESSED", ""
			if advanced {
				wantAdv = "PROCESSED"
			}
			sess, err := a.session()
			if err != nil {
				return a.fail("%v", err)
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), timeout+time.Minute)
			defer cancel()
			get := func(ctx context.Context) (*forward.Snapshot, error) { return sess.Snapshot(ctx, network, snapshot) }
			return a.exit(waitForSnapshot(ctx, get, snapshot, want, wantAdv, timeout, interval, time.Sleep, time.Now, a.out, a.err))
		},
	}
	c.Flags().StringVar(&network, "network", "", "network id")
	c.Flags().StringVar(&snapshot, "snapshot", "", "snapshot id")
	c.Flags().BoolVar(&advanced, "advanced-reachability", false, "also wait for the snapshot's advanced reachability to be PROCESSED")
	c.Flags().DurationVar(&timeout, "timeout", 2*time.Hour, "give up after this long (exit 2)")
	c.Flags().DurationVar(&interval, "interval", 30*time.Second, "time between polls (at least 1s)")
	return c
}

// waitForSnapshot polls get until the snapshot is in the wanted states (0), in a state that will not change by itself (1), or the timeout passes (2). Several polls in a row that
// error end it with 3, so a Forward outage is not waited out silently; one failed poll is not.
func waitForSnapshot(ctx context.Context, get func(context.Context) (*forward.Snapshot, error), id, want, wantAdv string, timeout, interval time.Duration,
	sleep func(time.Duration), now func() time.Time, stdout, stderr io.Writer) int {
	final := map[string]bool{"FAILED": true, "CANCELED": true, "TIMED_OUT": true}
	start := now()
	errs, idle := 0, 0
	emit := func(status, state, adv string, code int) int {
		b, _ := json.Marshal(map[string]any{"status": status, "snapshot_id": id, "state": state, "advanced_reachability": adv, "waited_seconds": int(now().Sub(start).Seconds())})
		fmt.Fprintln(stdout, string(b))
		return code
	}
	for {
		sn, err := get(ctx)
		elapsed := now().Sub(start)
		if err != nil {
			errs++
			fmt.Fprintf(stderr, "%s poll failed (%d in a row): %v\n", elapsed.Round(time.Second), errs, err)
			if errs >= 5 {
				fmt.Fprintf(stderr, "error: Forward could not be read for %d polls in a row; the wait stopped\n", errs)
				return 3
			}
		} else {
			errs = 0
			adv := sn.AdvancedReachabilityState
			if adv == "" {
				adv = "NOT_REPORTED"
			}
			fmt.Fprintf(stderr, "%s snapshot %s: state %s, advanced reachability %s\n", elapsed.Round(time.Second), id, sn.State, adv)
			// a state nothing is producing: the wanted work was never started
			if sn.State == "UNPROCESSED" || (sn.State == want && wantAdv != "" && adv == "UNPROCESSED") {
				if idle++; idle == 3 {
					if sn.State == "UNPROCESSED" {
						fmt.Fprintf(stderr, "snapshot %s is UNPROCESSED and nothing is processing it; this wait will not start it (edit-snapshot does)\n", id)
					} else {
						fmt.Fprintf(stderr, "advanced reachability of snapshot %s is UNPROCESSED: it was never started, or Forward has not yet begun it; this wait will not start it (edit-advanced-reachability does)\n", id)
					}
				}
			} else {
				idle = 0
			}
			switch {
			case final[sn.State] && sn.State != want:
				fmt.Fprintf(stderr, "snapshot %s ended in %s, which Forward does not change by itself; reprocess it to try again\n", id, sn.State)
				return emit("failed", sn.State, adv, waitFailed)
			case wantAdv != "" && sn.State == want && final[adv] && adv != wantAdv:
				fmt.Fprintf(stderr, "advanced reachability of snapshot %s ended in %s, which Forward does not change by itself\n", id, adv)
				return emit("failed", sn.State, adv, waitFailed)
			case sn.State == want && (wantAdv == "" || adv == wantAdv):
				return emit("ok", sn.State, adv, waitReached)
			}
		}
		if elapsed+interval > timeout {
			state, adv := "", ""
			if sn != nil {
				state, adv = sn.State, sn.AdvancedReachabilityState
			}
			fmt.Fprintf(stderr, "timed out after %s; the work may still be running: run the wait again\n", elapsed.Round(time.Second))
			return emit("timed_out", state, adv, waitTimedOut)
		}
		sleep(interval)
	}
}
