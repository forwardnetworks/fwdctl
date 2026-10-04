package skills

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	forward "github.com/forwardnetworks/forward-go-sdk"

	"github.com/forwardnetworks/fwdctl/fwd"
	"github.com/forwardnetworks/fwdctl/result"
)

const inspectPlatformName = "inspect-platform"

func init() { Register(inspectPlatformName, inspectPlatform) }

type inspectPlatformInput struct {
	NetworkID string `json:"network_id"`
	Area      string `json:"area"`
	Name      string `json:"name"`
	Limit     int    `json:"limit"`
	Offset    int    `json:"offset"`
}

// platformArea is one thing an administrator sets up or looks after. list reads it; network says whether it belongs to a network (true) or to the organization (false). A secret an
// SDK type carries is removed by name before anything is returned (fwd.RedactSecrets), so no area can leak one by being added carelessly.
type platformArea struct {
	network bool
	what    string // one line, for the error that lists the areas
	list    func(ctx context.Context, s *fwd.Session, networkID string) (rows []any, limits []string, err error)
}

func rowsOf[T any](items []T, err error) ([]any, error) {
	if err != nil {
		return nil, err
	}
	out := make([]any, 0, len(items))
	for _, it := range items {
		g, gerr := fwd.Generic(it)
		if gerr != nil {
			return nil, gerr
		}
		out = append(out, g)
	}
	return out, nil
}

func tag(rows []any, key, value string) []any {
	for _, r := range rows {
		if m, ok := r.(map[string]any); ok {
			m[key] = value
		}
	}
	return rows
}

var platformAreas = map[string]platformArea{
	"credentials": {true, "CLI, SNMP and HTTP credentials (names and ids; never the secrets)", func(ctx context.Context, s *fwd.Session, n string) ([]any, []string, error) {
		var out []any
		var lim []string
		cli, _, e1 := s.Client.Credentials.ListCLI(ctx, n)
		r1, err := rowsOf(cli, e1)
		if lim = soft(lim, "CLI credentials", err); err == nil {
			out = append(out, tag(r1, "credential_type", "CLI")...)
		}
		http, _, e2 := s.Client.Credentials.ListHTTP(ctx, n)
		r2, err := rowsOf(http, e2)
		if lim = soft(lim, "HTTP credentials", err); err == nil {
			out = append(out, tag(r2, "credential_type", "HTTP")...)
		}
		snmp, _, e3 := s.Client.Credentials.ListSNMP(ctx, n)
		r3, err := rowsOf(snmp, e3)
		if lim = soft(lim, "SNMP credentials", err); err == nil {
			out = append(out, tag(r3, "credential_type", "SNMP")...)
		}
		if len(lim) == 3 {
			return nil, nil, fmt.Errorf("%s", strings.Join(lim, "; "))
		}
		return out, append(lim, "secret values (passwords, keys, community strings) are never returned; a credential is named by its id, and the devices that use it are in inspect-collection view config"), nil
	}},
	"jump_servers": {true, "jump servers", func(ctx context.Context, s *fwd.Session, n string) ([]any, []string, error) {
		v, _, err := s.Client.JumpServers.List(ctx, n)
		r, err := rowsOf(v, err)
		return r, nil, err
	}},
	"proxies": {true, "proxy servers", func(ctx context.Context, s *fwd.Session, n string) ([]any, []string, error) {
		v, _, err := s.Client.Proxies.List(ctx, n)
		r, err := rowsOf(v, err)
		return r, nil, err
	}},
	"collectors": {false, "collectors and their status", func(ctx context.Context, s *fwd.Session, _ string) ([]any, []string, error) {
		v, _, err := s.Client.Collectors.List(ctx)
		r, err := rowsOf(v, err)
		return r, []string{"the organization's collection settings (timeouts, retries, concurrency defaults) are in inspect-collection view status and investigate-collection-failure view slow"}, err
	}},
	"endpoint_profiles": {false, "endpoint profiles (SNMP, CLI, HTTP), every one, used or not", func(ctx context.Context, s *fwd.Session, _ string) ([]any, []string, error) {
		v, _, err := s.Client.Endpoints.ListProfiles(ctx, "")
		r, err := rowsOf(v, err)
		return r, []string{"an HTTP profile's header values and any secret are removed; which endpoints use a profile is in inspect-collection view config"}, err
	}},
	"collection_settings": {false, "the organization's collection settings (timeouts, retries, concurrency, rates)", func(ctx context.Context, s *fwd.Session, _ string) ([]any, []string, error) {
		v, _, err := s.Client.Collectors.GetOrganizationSettings(ctx)
		if err != nil {
			return nil, nil, err
		}
		r, err := rowsOf([]*forward.OrgCollectionSettings{v}, nil)
		return r, []string{"a field that is absent is unset and Forward uses its default (a device collection timeout of 180 minutes, 2 retries); a collector's own concurrency is in inspect-collection view status"}, err
	}},
	"schedules": {true, "collection schedules", func(ctx context.Context, s *fwd.Session, n string) ([]any, []string, error) {
		v, _, err := s.Client.CollectionSchedules.List(ctx, n)
		r, err := rowsOf(v, err)
		return r, []string{"Forward does not return the next run time"}, err
	}},
	"cloud_setups": {true, "cloud accounts, controller-managed and Mist setups (no keys)", func(ctx context.Context, s *fwd.Session, n string) ([]any, []string, error) {
		var out []any
		var lim []string
		ca, _, e1 := s.Client.CloudAccounts.List(ctx, n)
		r1, err := rowsOf(ca, e1)
		if lim = soft(lim, "cloud accounts", err); err == nil {
			out = append(out, tag(r1, "setup_type", "cloud_account")...)
		}
		cm, _, e2 := s.Client.ControllerManagedSetups.List(ctx, n)
		r2, err := rowsOf(cm, e2)
		if lim = soft(lim, "controller-managed setups", err); err == nil {
			out = append(out, tag(r2, "setup_type", "controller_managed")...)
		}
		mi, _, e3 := s.Client.CloudManagedSetups.ListMist(ctx, n)
		r3, err := rowsOf(mi, e3)
		if lim = soft(lim, "Mist setups", err); err == nil {
			out = append(out, tag(r3, "setup_type", "mist")...)
		}
		if len(lim) == 3 {
			return nil, nil, fmt.Errorf("%s", strings.Join(lim, "; "))
		}
		return out, append(lim, "keys, private keys and passwords are never returned; a region's last connectivity test is in inspect-collection view config"), nil
	}},
	"locations": {true, "locations", func(ctx context.Context, s *fwd.Session, n string) ([]any, []string, error) {
		v, _, err := s.Client.Locations.List(ctx, n)
		r, err := rowsOf(v, err)
		return r, nil, err
	}},
	"tag_definitions": {true, "device tags defined on the network", func(ctx context.Context, s *fwd.Session, n string) ([]any, []string, error) {
		v, _, err := s.Client.DeviceTags.List(ctx, n, "")
		r, err := rowsOf(v, err)
		return r, nil, err
	}},
	"access_labels": {false, "device access labels", func(ctx context.Context, s *fwd.Session, _ string) ([]any, []string, error) {
		v, _, err := s.Client.AccessControl.ListDeviceAccessLabels(ctx)
		r, err := rowsOf(v, err)
		return r, nil, err
	}},
	"api_tokens": {false, "this login's API tokens (names and dates; never the secret)", func(ctx context.Context, s *fwd.Session, _ string) ([]any, []string, error) {
		v, _, err := s.Client.Users.ListTokens(ctx)
		r, err := rowsOf(v, err)
		return r, []string{"only this login's own tokens; another user's need the organization administrator role"}, err
	}},
	"webhooks": {false, "webhooks and their last test result", func(ctx context.Context, s *fwd.Session, _ string) ([]any, []string, error) {
		v, tests, _, err := s.Client.Webhooks.List(ctx)
		r, err := rowsOf(v, err)
		if err == nil && len(tests) > 0 {
			g, _ := fwd.Generic(tests)
			for _, row := range r {
				if m, ok := row.(map[string]any); ok {
					if id := fmt.Sprint(m["id"]); id != "" {
						if gm, ok := g.(map[string]any); ok {
							m["last_test"] = gm[id]
						}
					}
				}
			}
		}
		return r, []string{"the signing secret and any URL token are never returned"}, err
	}},
	"integrations": {true, "ServiceNow, Infoblox and Rapid7 integrations (no passwords)", func(ctx context.Context, s *fwd.Session, n string) ([]any, []string, error) {
		var out []any
		var lim []string
		ib, _, e1 := s.Client.Integrations.ListInfoblox(ctx)
		r1, err := rowsOf(ib, e1)
		if lim = soft(lim, "Infoblox", err); err == nil {
			out = append(out, tag(r1, "integration", "infoblox")...)
		}
		if sn, _, _, e2 := s.Client.Integrations.GetServiceNow(ctx); e2 == nil && sn != nil {
			if g, gerr := fwd.Generic(sn); gerr == nil {
				if m, ok := g.(map[string]any); ok {
					m["integration"] = "servicenow"
					out = append(out, m)
				}
			}
		}
		if n != "" {
			r7, _, e3 := s.Client.Integrations.ListRapid7(ctx, n)
			rr, err := rowsOf(r7, e3)
			if lim = soft(lim, "Rapid7", err); err == nil {
				out = append(out, tag(rr, "integration", "rapid7")...)
			}
		}
		return out, append(lim, "Rapid7 sources belong to a network: give network_id to include them; passwords are never returned"), nil
	}},
	"licensing": {false, "licenses of the organization", func(ctx context.Context, s *fwd.Session, _ string) ([]any, []string, error) {
		v, _, err := s.Client.Licensing.List(ctx)
		r, err := rowsOf(v, err)
		if err != nil {
			return nil, nil, err
		}
		lim := []string{"the signed license key is never returned; this is your own organization's licenses (another organization's are a Forward support task)"}
		if ts, _, terr := s.Client.Licensing.TierAndStatus(ctx); terr == nil && ts != nil {
			if g, gerr := fwd.Generic(ts); gerr == nil {
				if m, ok := g.(map[string]any); ok {
					m["summary"] = "tier and status"
					r = append([]any{m}, r...)
				}
			}
		}
		return r, lim, nil
	}},
	"backups": {false, "backup settings, storage and the last backup", func(ctx context.Context, s *fwd.Session, _ string) ([]any, []string, error) {
		var out []any
		var lim []string
		for _, st := range []forward.StorageType{forward.StorageTypeInternal, forward.StorageTypeS3} {
			b, _, err := s.Client.Backups.GetSettings(ctx, st)
			if err != nil {
				lim = append(lim, fmt.Sprintf("%s backup settings could not be read: %v", st, err))
				continue
			}
			if g, gerr := fwd.Generic(b); gerr == nil {
				out = append(out, map[string]any{"storage": string(st), "settings": g})
			}
		}
		if s3, _, err := s.Client.Backups.GetS3Storage(ctx); err == nil && s3 != nil {
			if g, gerr := fwd.Generic(s3); gerr == nil {
				out = append(out, map[string]any{"storage": "S3", "s3": g})
			}
		}
		if last, _, err := s.Client.Backups.Last(ctx, forward.StorageTypeInternal, forward.BackupTriggerScheduled); err == nil && last != nil {
			if g, gerr := fwd.Generic(last); gerr == nil {
				out = append(out, map[string]any{"last_scheduled_internal": g})
			}
		}
		return out, append(lim, "access and secret keys are never returned"), nil
	}},
	"banners": {false, "custom banners", func(ctx context.Context, s *fwd.Session, _ string) ([]any, []string, error) {
		v, _, err := s.Client.Banners.List(ctx)
		r, err := rowsOf(v, err)
		return r, nil, err
	}},
	"certificates": {false, "trusted certificates", func(ctx context.Context, s *fwd.Session, _ string) ([]any, []string, error) {
		v, _, err := s.Client.TrustedCertificates.List(ctx)
		r, err := rowsOf(v, err)
		return r, []string{"certificate bodies are shortened"}, err
	}},
	"saml": {false, "SAML single sign-on settings (status; no certificates)", func(ctx context.Context, s *fwd.Session, _ string) ([]any, []string, error) {
		v, _, err := s.Client.SAML.GetSettings(ctx)
		if err != nil {
			return nil, nil, err
		}
		if v == nil {
			return []any{}, []string{"no SAML is configured"}, nil
		}
		r, err := rowsOf([]*forward.SAMLSettings{v}, nil)
		return r, []string{"identity-provider certificates and metadata are shortened or hidden"}, err
	}},
	"organizations": {false, "organizations this login can see", func(ctx context.Context, s *fwd.Session, _ string) ([]any, []string, error) {
		v, _, err := s.Client.Organizations.List(ctx)
		r, err := rowsOf(v, err)
		return r, nil, err
	}},
	"dashboards": {true, "custom dashboards of the network (unpublished Forward API)", func(ctx context.Context, s *fwd.Session, n string) ([]any, []string, error) {
		v, _, err := s.Client.Dashboards.List(ctx, n)
		r, err := rowsOf(v, err)
		return r, []string{"read through an unpublished Forward API: the shape may change; the layout is the raw widget list"}, err
	}},
	"scorecards": {true, "scorecard definitions and the latest processed snapshot's scores (unpublished Forward API)", func(ctx context.Context, s *fwd.Session, n string) ([]any, []string, error) {
		lim := []string{"read through an unpublished Forward API; Forward computes scorecards for the organization's license tier"}
		defs, _, e1 := s.Client.Scorecards.Definitions(ctx, n)
		out, err := rowsOf(defs, e1)
		if err != nil {
			return nil, lim, err
		}
		out = tag(out, "row", "definition")
		snap, err := resolveSnapshot(ctx, s, n, "")
		if err != nil || snap == nil || !fwd.IsReady(snap) {
			return out, append(lim, "no processed snapshot, so no scores were read"), nil
		}
		scores, _, e2 := s.Client.Scorecards.ForSnapshot(ctx, n, string(snap.ID))
		r2, err := rowsOf(scores, e2)
		if lim = soft(lim, "scores on snapshot "+string(snap.ID), err); err == nil {
			out = append(out, tag(r2, "row", "score")...)
		}
		return out, lim, nil
	}},
	"scorecard_trends": {true, "each scorecard's score over the last 90 days (unpublished Forward API)", func(ctx context.Context, s *fwd.Session, n string) ([]any, []string, error) {
		end := time.Now().UTC()
		trends, _, err := s.Client.Scorecards.Trends(ctx, n, end.AddDate(0, 0, -90), end, 60)
		if err != nil {
			return nil, nil, err
		}
		names := map[string]string{}
		if defs, _, derr := s.Client.Scorecards.Definitions(ctx, n); derr == nil {
			for _, d := range defs {
				names[string(d.ID)] = d.Name
			}
		}
		ids := make([]string, 0, len(trends))
		for id := range trends {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		var out []any
		for _, id := range ids {
			pts := trends[id]
			row := map[string]any{"scorecard_id": id, "name": names[id], "points": len(pts)}
			var scored []forward.ScorecardPoint
			for _, p := range pts {
				if p.Score != nil {
					scored = append(scored, p)
				}
			}
			row["scored_points"] = len(scored)
			if len(scored) > 0 {
				lo, hi := *scored[0].Score, *scored[0].Score
				for _, p := range scored {
					lo, hi = min(lo, *p.Score), max(hi, *p.Score)
				}
				first, last := scored[0], scored[len(scored)-1]
				row["first"] = map[string]any{"time": first.Time.Format(time.RFC3339), "snapshot_id": string(first.SnapshotID), "score": *first.Score}
				row["latest"] = map[string]any{"time": last.Time.Format(time.RFC3339), "snapshot_id": string(last.SnapshotID), "score": *last.Score}
				row["min"], row["max"], row["change"] = lo, hi, *last.Score-*first.Score
			}
			out = append(out, row)
		}
		return out, []string{"read through an unpublished Forward API; a window of the last 90 days, at most 60 points per scorecard; points with no score (no data for that snapshot) are counted but not averaged", "a score is Forward's own calculation for the organization's license tier"}, nil
	}},
	"cve_index": {false, "the vulnerability (CVE) index", func(ctx context.Context, s *fwd.Session, _ string) ([]any, []string, error) {
		m, _, err := s.Client.CVEIndex.Metadata(ctx)
		if err != nil {
			return nil, nil, err
		}
		r, err := rowsOf([]*forward.CVEIndexMetadata{m}, nil)
		return r, nil, err
	}},
}

func platformAreaNames() []string {
	out := make([]string, 0, len(platformAreas))
	for k := range platformAreas {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// inspectPlatform reads what an administrator sets up and looks after, one area at a time, with every secret removed: credentials, jump servers, proxies, collectors, schedules,
// cloud and controller setups, locations, tag definitions, access labels, API tokens, webhooks, integrations, licensing, backups, banners, trusted certificates, SAML, organizations
// and the CVE index. It reads only; the edit skills change these.
func inspectPlatform(ctx context.Context, s *fwd.Session, raw json.RawMessage) (result.Result, error) {
	var in inspectPlatformInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return result.Result{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	a, ok := platformAreas[in.Area]
	if !ok {
		return result.Result{}, fmt.Errorf("%w: area is one of %s", ErrInvalidInput, strings.Join(platformAreaNames(), ", "))
	}
	cx := result.Context{NetworkID: in.NetworkID, Scope: "account", State: "current"}
	if a.network && in.NetworkID == "" && in.Area != "integrations" {
		return result.Result{}, fmt.Errorf("%w: area %s belongs to a network: give network_id", ErrInvalidInput, in.Area)
	}
	if !a.network && in.NetworkID != "" && in.Area != "integrations" {
		return result.Result{}, fmt.Errorf("%w: area %s belongs to the organization, not a network: leave network_id out", ErrInvalidInput, in.Area)
	}
	rows, limits, err := a.list(ctx, s, in.NetworkID)
	if err != nil {
		if r, ok := denialResult(inspectPlatformName, err, cx, "could not read "+a.what); ok {
			return r, nil
		}
		// a login without the role, a Forward build without the route, or a route that wants other parameters is a fact about this read, not a failure of the tool
		if st := fwd.Status(err); st == 400 || st == 403 || st == 404 || st == 405 || st == 501 {
			return result.NewUnknown(inspectPlatformName, fmt.Sprintf("Could not read %s", a.what), cx,
				[]string{fmt.Sprintf("Forward answered %d: %v", st, err), "this login lacks the role, or this Forward build does not serve that route; nothing was read, which is not proof the area is empty (inspect-access view explain names the role)"}, result.Options{NextActions: []string{"inspect-access"}})
		}
		return result.Result{}, err
	}
	redacted := 0
	for _, r := range rows {
		redacted += fwd.RedactSecrets(r)
	}
	if in.Name != "" {
		kept := rows[:0:0]
		for _, r := range rows {
			if m, ok := r.(map[string]any); ok && strings.Contains(strings.ToLower(fmt.Sprint(m["name"])), strings.ToLower(in.Name)) {
				kept = append(kept, r)
			}
		}
		rows = kept
	}
	if redacted > 0 {
		limits = append(limits, fmt.Sprintf("%d secret value(s) were present in Forward's answer and were replaced with <redacted>; no secret is ever returned by this skill", redacted))
	}
	limits = append(limits, "this reads what Forward holds now; it does not test that a credential, account or webhook works (the edit skills' test actions do)")
	if len(rows) == 0 {
		return result.NewUnknown(inspectPlatformName, fmt.Sprintf("No %s returned", a.what), cx, append(limits, "an empty list is what Forward returned to this login: another login with wider roles may see more"), result.Options{NextActions: []string{"inspect-access"}})
	}
	plain := make([]map[string]any, 0, len(rows))
	for _, r := range rows {
		if m, ok := r.(map[string]any); ok {
			plain = append(plain, m)
		} else {
			plain = append(plain, map[string]any{"value": r})
		}
	}
	win, wl, wok := window(plain, in.Limit, in.Offset, 50, 500, "rows")
	if !wok {
		return result.NewUnknown(inspectPlatformName, fmt.Sprintf("Offset %d is beyond the %d rows", in.Offset, len(plain)), cx, []string{"offset is past the end of the list"}, result.Options{})
	}
	finding := fmt.Sprintf("%d %s", len(plain), a.what)
	d := map[string]any{"area": in.Area, "total": len(plain), "offset": in.Offset, "rows": win}
	return result.Build(inspectPlatformName, result.OK, finding, result.Deterministic, cx, result.Options{Limits: append(wl, limits...), NextActions: []string{"inspect-access", "inspect-collection"},
		Evidence: []result.Evidence{result.NewEvidence(result.EvState, "platform_"+in.Area, nil, d, finding)}})
}

// soft records that one of several reads in an area failed, without failing the area: the rows that could be read are still returned.
func soft(lim []string, what string, err error) []string {
	if err == nil {
		return lim
	}
	return append(lim, fmt.Sprintf("%s could not be read (%v)", what, err))
}
