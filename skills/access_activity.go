package skills

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	forward "github.com/forwardnetworks/forward-go-sdk"

	"github.com/forwardnetworks/fwdctl/fwd"
	"github.com/forwardnetworks/fwdctl/result"
)

const (
	defaultActivityWindow = 7 * 24 * time.Hour
	activityFetch         = 2000 // records read to summarise; the rows shown are a page of them
)

var sinceRe = regexp.MustCompile(`^(\d+)([dhm])$`)

// parseSince reads "7d", "48h", "90m" or an RFC 3339 time as a start time; now is injected for tests.
func parseSince(s string, now time.Time) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return now.Add(-defaultActivityWindow), nil
	}
	if m := sinceRe.FindStringSubmatch(s); m != nil {
		n, _ := strconv.Atoi(m[1])
		unit := map[string]time.Duration{"d": 24 * time.Hour, "h": time.Hour, "m": time.Minute}[m[2]]
		return now.Add(-time.Duration(n) * unit), nil
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	return time.Time{}, fmt.Errorf("%w: since is a span such as 7d, 48h or 90m, or an RFC 3339 time", ErrInvalidInput)
}

// accessActivity is view activity: who called which Forward route, when, from where and with what result, from Forward's audit log. It records HTTP requests, not what they
// changed, so it answers "who touched the device list and when", not "which devices".
func accessActivity(ctx context.Context, s *fwd.Session, in inspectAccessInput, cx result.Context) (result.Result, error) {
	start, err := parseSince(in.Since, time.Now())
	if err != nil {
		return result.Result{}, err
	}
	method := strings.ToUpper(strings.TrimSpace(in.Method))
	switch method {
	case "", "POST", "PUT", "PATCH", "DELETE", "GET":
	default:
		return result.Result{}, fmt.Errorf("%w: method is POST, PUT, PATCH, DELETE or GET", ErrInvalidInput)
	}
	// the route prefix: network_id is a shorthand for /networks/<id>, with match as the rest of the path
	prefix := strings.TrimSpace(in.Match)
	if in.NetworkID != "" {
		prefix = "/networks/" + in.NetworkID + "/" + strings.TrimPrefix(prefix, "/")
		prefix = strings.TrimSuffix(prefix, "/")
	}
	// who: names are resolved both ways, so a user may be given by id, username or email and the rows show names
	names := map[string]string{}
	var limits []string
	users, _, uerr := s.Client.Users.ListWithRoles(ctx)
	if uerr != nil {
		limits = append(limits, "users could not be listed, so records show user ids and --user must be an id: "+uerr.Error())
	}
	for _, u := range users {
		names[string(u.ID)] = u.Username
	}
	userID := ""
	if in.User != "" {
		want := strings.ToLower(strings.TrimSpace(in.User))
		var hit []string
		for _, u := range users {
			if strings.EqualFold(string(u.ID), want) || strings.EqualFold(u.Username, want) || strings.EqualFold(u.Email, want) {
				hit = append(hit, string(u.ID))
			}
		}
		switch {
		case len(hit) == 1:
			userID = hit[0]
		case len(hit) > 1:
			return result.Result{}, fmt.Errorf("%w: user %q matches %d users; give the id", ErrInvalidInput, in.User, len(hit))
		case uerr != nil:
			userID = in.User
		default:
			return result.NewUnknown(inspectAccessName, fmt.Sprintf("No user %q", in.User), cx, []string{fmt.Sprintf("%d users were read; none has that id, username or email", len(users))}, result.Options{})
		}
	}
	logs, _, err := s.Client.AuditLogs.List(ctx, forward.AuditLogListOptions{StartTime: start, HTTPMethod: method, TargetURI: prefix, UserID: userID, Limit: activityFetch})
	if err != nil {
		return accessError(err, cx, "read the audit log")
	}
	recs := logs.Records
	limits = append(limits,
		"the audit log records who called which route, from which address, when, and with what response code; it records no request bodies, so it cannot say WHICH devices or settings a call changed",
		"it records POST, PUT, PATCH and DELETE, and a GET only on the few routes marked for auditing; a route can opt out of auditing, so no record is not proof nothing happened. Collector requests and failed logins are not in it, and only authenticated users are",
		"a 4xx or 5xx code is an attempt that failed; a 2xx DELETE on a device route is a deletion. Sensitive query values (password, token, secret) are stored as <redacted>, and a route longer than 2048 characters is cut",
		"route is matched as a case-insensitive PREFIX of the stored path, which has no /api in front; a match on 'contains' or 'ends with' is not possible, so filter by prefix and method and read the rows")
	if len(recs) == 0 {
		return result.NewUnknown(inspectAccessName, "No audited request matches", cx, append(limits, fmt.Sprintf("the window starts %s; widen since, loosen the route prefix or method, or ask a login that may view the audit log", start.Format("2006-01-02 15:04 UTC"))), result.Options{})
	}
	byUser, byMethod, byClass := map[string]int{}, map[string]int{}, map[string]int{}
	rows := make([]map[string]any, 0, len(recs))
	for _, r := range recs {
		who := firstNonEmpty(names[string(r.UserID)], string(r.UserID), "(unauthenticated)")
		byUser[who]++
		byMethod[r.HTTPMethod]++
		byClass[strconv.Itoa(r.HTTPResponseCode/100)+"xx"]++
		row := map[string]any{"time": r.Time.UTC().Format(time.RFC3339), "user": who, "method": r.HTTPMethod, "route": r.TargetURI, "status": r.HTTPResponseCode, "from": r.RemoteIP}
		if r.Impersonated {
			row["impersonated"] = true
		}
		rows = append(rows, row)
	}
	total := logs.Paging.Total
	if total > int64(len(recs)) {
		limits = append(limits, fmt.Sprintf("%d audited requests match; the newest %d were read and summarised (by_user, by_method and by_outcome cover those)", total, len(recs)))
	}
	win, wl, ok := window(rows, in.Limit, in.Offset, 25, 200, "requests")
	if !ok {
		return result.NewUnknown(inspectAccessName, fmt.Sprintf("Offset %d is beyond the %d requests read", in.Offset, len(rows)), cx, []string{"offset is past the end of the list"}, result.Options{})
	}
	limits = append(limits, wl...)
	finding := fmt.Sprintf("%d audited request(s) since %s", total, start.Format("2006-01-02"))
	if prefix != "" {
		finding += " under " + prefix
	}
	if k, n := biggest(byUser); n > 0 {
		finding += fmt.Sprintf("; most by %s (%d)", k, n)
	}
	if failed := byClass["4xx"] + byClass["5xx"]; failed > 0 {
		finding += fmt.Sprintf("; %d failed (4xx/5xx)", failed)
	}
	finding += fmt.Sprintf("; newest %s, oldest read %s", rows[0]["time"], rows[len(rows)-1]["time"])
	detail := map[string]any{"total_matching": total, "read": len(recs), "since": start.UTC().Format(time.RFC3339), "route_prefix": prefix, "method": method,
		"by_user": topDeviceCountRowsNamed(byUser, 10, "requests"), "by_method": byMethod, "by_outcome": byClass, "offset": in.Offset, "requests": win}
	return result.Build(inspectAccessName, result.OK, finding, result.Deterministic, cx, result.Options{Limits: limits, NextActions: []string{"inspect-access", "inspect-collection"},
		Evidence: []result.Evidence{result.NewEvidence(result.EvState, "getAuditLogs", nil, detail, finding)}})
}

// topDeviceCountRowsNamed is the n biggest groups as rows with the count under the given key.
func topDeviceCountRowsNamed(m map[string]int, n int, key string) []map[string]any {
	names := make([]string, 0, len(m))
	for k := range m {
		names = append(names, k)
	}
	sort.Slice(names, func(i, j int) bool {
		if m[names[i]] != m[names[j]] {
			return m[names[i]] > m[names[j]]
		}
		return names[i] < names[j]
	})
	out := make([]map[string]any, 0, min(len(names), n))
	for i, k := range names {
		if i == n {
			break
		}
		out = append(out, map[string]any{"name": k, key: m[k]})
	}
	return out
}
