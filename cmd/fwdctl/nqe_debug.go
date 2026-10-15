package main

import (
	"encoding/json"
	"fmt"
	"time"

	forward "github.com/forwardnetworks/forward-go-sdk"
	"github.com/spf13/cobra"

	"github.com/forwardnetworks/fwdctl/fwd"
)

// nqeExplain prints Forward's optimized plan for a query: the Query Debug Panel's text. It shows whether the query runs device-parallel (the first line starts parallel_foreach), where a lookup replaced a
// loop (_lookup), and which expressions the optimizer found total or of ONCE cardinality. Forward explains a SAVED library query (or a draft at a path in your workspace), not raw text.
func (a *app) nqeExplain() *cobra.Command {
	var network, queryID, commitID, path, snapshot, paramsFile string
	var params []string
	c := &cobra.Command{
		Use: "explain", Short: "EXPERIMENTAL: Forward's optimized plan for a saved query (the Query Debug Panel's text)",
		Long: "Prints the optimized plan Forward builds for a saved library query, as the Query Debug Panel in the NQE Library shows it: a device-parallel query starts `parallel_foreach`, a join that became a lookup shows\n" +
			"`_lookup(...)`, and `⸢ ⸣` marks expressions found total (cannot raise an error, so a `where` may float up), `⟪ ⟫` marks ONCE cardinality. Read it to see why a query is slow.\n" +
			"Give --query-id (optionally --commit-id; HEAD otherwise) or --path (a query in your own workspace, as the UI writes it, for example \"/Operations/my query\"). Raw query text is not explained by Forward: save it\n" +
			"as a draft first. Internal Forward routes (preview); needs the organization's VIEW_NQE_LIBRARY permission. A query with errors is refused with its diagnostics.\n" +
			"For the execution time use `fwdctl nqe run --meta`; for slow runs Forward recorded, `fwdctl nqe metrics`.",
		Example: "  fwdctl nqe explain --query-id Q_0123456789abcdef0123456789abcdef01234567 --network 123\n  fwdctl nqe explain --path \"/Operations/my query\" --param threshold=3",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if (queryID == "") == (path == "") {
				return a.fail("give --query-id (a saved query) or --path (a query in your workspace), not both")
			}
			if queryID != "" {
				if p := queryIDProblem(queryID); p != "" {
					return a.fail("%s", p)
				}
			}
			p, err := nqeParams(paramsFile, params)
			if err != nil {
				return a.fail("%v", err)
			}
			sess, err := a.session()
			if err != nil {
				return a.fail("%v", err)
			}
			if snapshot == "" && network != "" {
				if s, serr := sess.LatestProcessed(cmd.Context(), network); serr == nil && s != nil {
					snapshot = string(s.ID)
				}
			}
			var plan string
			if queryID != "" {
				plan, _, err = sess.Client.NQE.ExplainQuery(cmd.Context(), queryID, commitID, snapshot, p)
			} else {
				plan, _, err = sess.Client.NQE.ExplainPath(cmd.Context(), path, snapshot, p)
			}
			if err != nil {
				fmt.Fprintf(a.err, "error: %v\n", err)
				return a.exit(3)
			}
			fmt.Fprint(a.out, plan)
			if len(plan) > 0 && plan[len(plan)-1] != '\n' {
				fmt.Fprintln(a.out)
			}
			return nil
		},
	}
	f := c.Flags()
	f.StringVar(&network, "network", "", "network id (its newest processed snapshot supplies the schema; optional)")
	f.StringVar(&snapshot, "snapshot", "", "snapshot id to take the schema from (optional)")
	f.StringVar(&queryID, "query-id", "", "a saved library query's id")
	f.StringVar(&commitID, "commit-id", "", "the commit of the saved query (default HEAD)")
	f.StringVar(&path, "path", "", "a query in your own NQE workspace, without the repository, for example \"/Operations/my query\"")
	f.StringVar(&paramsFile, "params", "", "a JSON file of query parameters")
	f.StringArrayVar(&params, "param", nil, "a query parameter NAME=VALUE (repeatable)")
	return c
}

// nqeMetrics lists the query runs Forward recorded as slow.
func (a *app) nqeMetrics() *cobra.Command {
	var minSeconds, limit int
	var from, to string
	c := &cobra.Command{
		Use: "metrics", Short: "EXPERIMENTAL: the slow query runs Forward recorded (run time, network, query)",
		Long: "Lists NQE runs Forward recorded with their run duration (execution only, not queue time), slowest first as Forward returns them: task, network, snapshot, saved query id and commit when it was a saved query,\n" +
			"when it ran. Defaults are Forward's: the last 7 days, runs of at least 10 seconds, at most 100. --min-seconds 0 lists every run. This is a Forward debug feature (organization permission DEBUG_NQE), which most\n" +
			"logins lack; the error names it. Use it to find which queries are expensive; `fwdctl nqe explain` shows why.",
		Example: "  fwdctl nqe metrics --min-seconds 30 --limit 20",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			opt := forward.NQERunMetricsOptions{}
			if cmd.Flags().Changed("min-seconds") {
				opt.MinExecutionSeconds = &minSeconds
			}
			if cmd.Flags().Changed("limit") {
				opt.Limit = &limit
			}
			for _, x := range []struct {
				v   string
				to  *time.Time
				nam string
			}{{from, &opt.From, "--from"}, {to, &opt.To, "--to"}} {
				if x.v == "" {
					continue
				}
				t, err := time.Parse(time.RFC3339, x.v)
				if err != nil {
					return a.fail("%s must be an RFC 3339 time such as 2026-10-01T00:00:00Z", x.nam)
				}
				*x.to = t
			}
			sess, err := a.session()
			if err != nil {
				return a.fail("%v", err)
			}
			ms, _, err := sess.Client.NQE.RunMetrics(cmd.Context(), opt)
			if err != nil {
				if op, ok := fwd.MissingPermission(err); ok {
					fmt.Fprintf(a.err, "error: this login lacks %s, a Forward debug permission most logins do not have\n", op)
					return a.exit(3)
				}
				fmt.Fprintf(a.err, "error: %v\n", err)
				return a.exit(3)
			}
			rows := make([]map[string]any, 0, len(ms))
			for _, m := range ms {
				row := map[string]any{"task_id": m.TaskID, "network_id": m.NetworkID, "network_name": m.NetworkName, "snapshot_id": m.SnapshotID, "executed_at": m.ExecutedAt.UTC().Format(time.RFC3339),
					"run_seconds": m.RunDuration.Seconds(), "source_set_id": m.SourceSetID, "tracking_id": m.TrackingID}
				if m.QueryID != "" {
					row["query_id"], row["commit_id"] = m.QueryID, m.CommitID
				}
				rows = append(rows, row)
			}
			b, _ := json.MarshalIndent(map[string]any{"runs": rows, "count": len(rows), "note": "run_seconds is execution time only (not queue or analysis time); a run without query_id was sent as text"}, "", "  ")
			fmt.Fprintln(a.out, string(b))
			return nil
		},
	}
	f := c.Flags()
	f.IntVar(&minSeconds, "min-seconds", 10, "only runs of at least this many seconds (0: every run)")
	f.IntVar(&limit, "limit", 100, "at most this many runs")
	f.StringVar(&from, "from", "", "start of the window, RFC 3339 (default: 7 days ago)")
	f.StringVar(&to, "to", "", "end of the window, RFC 3339 (default: now)")
	return c
}
