package skills

import (
	"context"
	"fmt"
	"sort"
	"strings"

	forward "github.com/forwardnetworks/forward-go-sdk"

	"github.com/forwardnetworks/fwdctl/fwd"
	"github.com/forwardnetworks/fwdctl/result"
)

// invCloudCoverageRows reads every cloud account of a snapshot with what was collected for it. An account that was not collected has an empty name and zero VPCs; its id is the
// subscription or account id, which is also the key the owning setup uses for its own records.
const invCloudCoverageRows = `foreach account in network.cloudAccounts
select {
  ID: account.id,
  Account: account.name,
  Cloud: account.cloudType,
  Collected: account.collected,
  "Cloud setup": account.cloudSetupId,
  VPCs: length(account.vpcs),
  Subnets: sum(foreach vpc in account.vpcs select length(vpc.subnets)),
  Instances: sum(foreach vpc in account.vpcs select length(vpc.computeInstances))
}`

const maxUncollectedShown = 25

// inventoryCloudCoverage is kind cloud_coverage: the snapshot's cloud accounts rolled up by cloud setup, and for each account that was not collected what the setup itself holds about it.
// A reason is the setup's record (an Azure subscription's last connectivity test, an AWS member account's enabled flag and last error, a C2S portal account list), never a diagnosis.
func inventoryCloudCoverage(ctx context.Context, s *fwd.Session, in inventoryInput, cx result.Context, limits []string) (result.Result, error) {
	rows, nqeTotal, truncated, err := s.RunNQEAll(ctx, in.NetworkID, fwd.SnapshotID(cx), invCloudCoverageRows, 20000)
	if err != nil {
		return result.Result{}, err
	}
	if truncated {
		limits = append(limits, fmt.Sprintf("this read the first %d of %d cloud accounts, so every count here is low", len(rows), nqeTotal))
	}
	if len(rows) == 0 {
		return result.NewUnknown(inventoryName, "This snapshot has no cloud accounts, so there is no cloud coverage to report", cx,
			append(limits, "no cloud account was in the snapshot's data model: the network may have no cloud setup, or none was collected"), result.Options{NextActions: []string{"inspect-collection"}})
	}
	setups := map[string]forward.CloudAccount{}
	if list, _, lerr := s.Client.CloudAccounts.List(ctx, in.NetworkID); lerr != nil {
		limits = append(limits, "the cloud setups could not be read, so no reason is given for an uncollected account: "+lerr.Error())
	} else {
		for _, a := range list {
			setups[a.Name] = a
		}
	}
	type roll struct {
		accounts, collected, vpcs, subnets, instances int
		reasons                                       map[string]int
		uncollected                                   []map[string]any
	}
	by, order := map[string]*roll{}, []string{}
	total, collected := 0, 0
	for _, r := range rows {
		name := str(r["Cloud setup"])
		g := by[name]
		if g == nil {
			g = &roll{reasons: map[string]int{}}
			by[name] = g
			order = append(order, name)
		}
		g.accounts++
		total++
		if n, ok := num(r["VPCs"]); ok {
			g.vpcs += int(n)
		}
		if n, ok := num(r["Subnets"]); ok {
			g.subnets += int(n)
		}
		if n, ok := num(r["Instances"]); ok {
			g.instances += int(n)
		}
		if c, ok := r["Collected"].(bool); ok && c {
			g.collected++
			collected++
			continue
		}
		id := str(r["ID"])
		reason, known := uncollectedReason(setups, name, id)
		g.reasons[reason]++
		row := map[string]any{"id": id, "reason": reason}
		if known != "" {
			row["name"] = known
		}
		g.uncollected = append(g.uncollected, row)
	}
	sort.SliceStable(order, func(i, j int) bool {
		ni, nj := by[order[i]].accounts-by[order[i]].collected, by[order[j]].accounts-by[order[j]].collected
		if ni != nj {
			return ni > nj
		}
		return order[i] < order[j]
	})
	out := make([]map[string]any, 0, len(order))
	for _, name := range order {
		g := by[name]
		shown, _ := result.CapRow(g.uncollected, maxUncollectedShown)
		row := map[string]any{"setup": name, "accounts": g.accounts, "collected": g.collected, "not_collected": g.accounts - g.collected,
			"vpcs": g.vpcs, "subnets": g.subnets, "instances": g.instances, "reasons": g.reasons, "uncollected": shown, "uncollected_shown": len(shown)}
		if st, ok := setups[name]; ok {
			row["type"] = st.Type
		}
		out = append(out, row)
	}
	detail := map[string]any{"accounts": total, "collected": collected, "not_collected": total - collected, "setups": out}
	finding := fmt.Sprintf("%d cloud account(s) across %d setup(s), all collected", total, len(out))
	next := []string{"inspect-collection"}
	if collected < total {
		finding = fmt.Sprintf("%d of %d cloud account(s) were NOT collected across %d setup(s)", total-collected, total, len(out))
		next = []string{"inspect-collection", "investigate-collection-failure", "edit-source"}
		limits = append(limits,
			"an uncollected account has an empty name and zero VPCs in the snapshot because Forward holds no data for it; its id is the subscription or account id",
			"a reason is what the cloud setup itself records about the account, not a diagnosis: a passing connectivity test that still was not collected says only that. Run the setup's connectivity test (edit-source object cloud_account action test) to refresh it",
			fmt.Sprintf("uncollected shows at most %d accounts per setup; reasons counts all of them", maxUncollectedShown))
	}
	if collected < total {
		limits = append(limits, "what the collector itself reported failing for these accounts (a portal credentials request, an expired portal certificate, an Azure subscription it cannot see) is in investigate-collection-failure view cloud")
	}
	limits = append(limits, "the setups' records are read as they are now; the snapshot is as it was when collected, so an account added or tested since shows its newer state")
	return result.Build(inventoryName, result.OK, finding, result.Deterministic, cx, result.Options{Limits: limits, NextActions: next,
		Evidence: []result.Evidence{result.NewEvidence(result.EvState, "runNqeQuery", cx.SnapshotID, detail, finding)}})
}

// uncollectedReason says what a cloud setup holds about an account that was not collected, and the account's name when the setup knows it.
func uncollectedReason(setups map[string]forward.CloudAccount, setup, id string) (reason, name string) {
	st, ok := setups[setup]
	if !ok {
		return "its cloud setup was not found among the network's cloud setups", ""
	}
	in := func(l []string) bool {
		for _, x := range l {
			if x == id {
				return true
			}
		}
		return false
	}
	switch {
	case st.Type == "AZURE" && len(st.SubscriptionIDs) > 0 && !in(st.SubscriptionIDs):
		return "not selected: the setup collects only the subscriptions in its subscription list", ""
	case st.Type == "GCP" && len(st.ProjectIDs) > 0 && !in(st.ProjectIDs):
		return "not selected: the setup collects only the projects in its project list", ""
	case st.Type == "AZURE":
		t, has := st.TestResults[id]
		switch {
		case !has:
			return "no connectivity test result for this subscription", ""
		case t.Error == "" || t.Error == "NONE":
			return "connectivity test passes; not collected in this snapshot", ""
		}
		return "connectivity test failed: " + t.Error, ""
	case st.HasCAPCredential():
		for _, a := range st.AWSC2SAccountInfos {
			if a.AccountID == id {
				return "in the portal account list; not collected in this snapshot", ""
			}
		}
		return "not in the portal account list (c2s_accounts)", ""
	case st.Type == "AWS":
		for _, m := range st.AssumeRoleInfos {
			if m.AccountID != id {
				continue
			}
			switch {
			case !m.Enabled:
				return "disabled in the setup", m.AccountName
			case m.ErrorMsg != "":
				return "enabled; last error: " + oneLineNote(m.ErrorMsg), m.AccountName
			}
			return "enabled; no error recorded; not collected in this snapshot", m.AccountName
		}
		return "not in the setup's account list", ""
	}
	return "not collected; the " + strings.ToLower(st.Type) + " setup holds no per-account record", ""
}
