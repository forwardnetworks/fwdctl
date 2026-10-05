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
	activityPage          = forward.AuditLogMaxLimit
	activityMaxRecords    = 30000 // records read to summarise; the rows shown are a page of them
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
	var until time.Time
	if in.Until != "" {
		if until, err = parseSince(in.Until, time.Now()); err != nil {
			return result.Result{}, fmt.Errorf("%w: until is a span back from now (2d), or an RFC 3339 time", ErrInvalidInput)
		}
		if !until.After(start) {
			return result.Result{}, fmt.Errorf("%w: until must be after since", ErrInvalidInput)
		}
	}
	status := strings.ToLower(strings.TrimSpace(in.Status))
	switch status {
	case "", "ok", "failed":
	default:
		return result.Result{}, fmt.Errorf("%w: status is ok (2xx and 3xx) or failed (4xx and 5xx)", ErrInvalidInput)
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
	var all []forward.AuditLogRecord
	var total int64
	for len(all) < activityMaxRecords {
		page, _, perr := s.Client.AuditLogs.List(ctx, forward.AuditLogListOptions{StartTime: start, EndTime: until, HTTPMethod: method, TargetURI: prefix, UserID: userID, Limit: activityPage, Offset: len(all)})
		if perr != nil {
			return accessError(perr, cx, "read the audit log")
		}
		total = page.Paging.Total
		all = append(all, page.Records...)
		if len(page.Records) < activityPage || int64(len(all)) >= total {
			break
		}
	}
	readAll := int64(len(all)) >= total
	recs := all
	if status != "" {
		kept := recs[:0:0]
		for _, r := range recs {
			if failed := r.HTTPResponseCode >= 400; failed == (status == "failed") {
				kept = append(kept, r)
			}
		}
		recs = kept
	}
	limits = append(limits,
		"the audit log records who called which route, from which address, when, and with what response code; it records no request bodies, so it cannot say WHICH devices or settings a call changed",
		"it records POST, PUT, PATCH and DELETE, and a GET only on the few routes marked for auditing; a route can opt out of auditing, so no record is not proof nothing happened. Collector requests and failed logins are not in it, and only authenticated users are",
		"a 4xx or 5xx code is an attempt that failed; a 2xx DELETE on a device route is a deletion. Sensitive query values (password, token, secret) are stored as <redacted>, and a route longer than 2048 characters is cut",
		"the log records no request or response sizes and no item counts, so a batch call (deleteBatch, a POST of many devices) cannot be sized from it; what a batch changed is read by diffing the device lists of two snapshots (inspect-inventory kind devices with compare_to_snapshot_id)",
		"route is matched as a case-insensitive PREFIX of the stored path, which has no /api in front; a match on 'contains' or 'ends with' is not possible, so filter by prefix and method and read the rows")
	if len(recs) == 0 {
		return result.NewUnknown(inspectAccessName, "No audited request matches", cx, append(limits, fmt.Sprintf("the window starts %s; widen since, loosen the route prefix or method, or ask a login that may view the audit log", start.UTC().Format("2006-01-02 15:04 UTC"))), result.Options{})
	}
	unresolved := map[string]bool{}
	byUser, byMethod, byClass, byDay, byRoute := map[string]int{}, map[string]int{}, map[string]int{}, map[string]int{}, map[string]int{}
	rows := make([]map[string]any, 0, len(recs))
	for _, r := range recs {
		who := firstNonEmpty(names[string(r.UserID)], string(r.UserID), "(unauthenticated)")
		if names[string(r.UserID)] == "" && r.UserID != "" {
			unresolved[string(r.UserID)] = true
		}
		byUser[who]++
		byMethod[r.HTTPMethod]++
		byClass[strconv.Itoa(r.HTTPResponseCode/100)+"xx"]++
		byDay[r.Time.UTC().Format("2006-01-02")]++
		byRoute[r.HTTPMethod+" "+routeShape(r.TargetURI)]++
		row := map[string]any{"time": r.Time.UTC().Format(time.RFC3339), "user": who, "method": r.HTTPMethod, "route": r.TargetURI, "status": r.HTTPResponseCode, "from": r.RemoteIP}
		if r.Impersonated {
			row["impersonated"] = true
		}
		rows = append(rows, row)
	}
	if len(unresolved) > 0 && uerr == nil {
		limits = append(limits, fmt.Sprintf("%d user id(s) in these records are not in the user list and are shown as ids: a deleted user, an API token or service account, or a login the user list does not return", len(unresolved)))
	}
	if !readAll {
		limits = append(limits, fmt.Sprintf("%d audited requests match the time, route, method and user filters; the newest %d were read, so every count below covers only those (narrow since or until to see the rest)", total, len(all)))
	}
	if status != "" {
		limits = append(limits, fmt.Sprintf("status %s is applied here after reading, to the records read", status))
	}
	win, omitted, ok := window(rows, in.Limit, in.Offset, 25, 200, "requests")
	if !ok {
		return result.NewUnknown(inspectAccessName, fmt.Sprintf("Offset %d is beyond the %d requests read", in.Offset, len(rows)), cx, []string{"offset is past the end of the list"}, result.Options{})
	}
	finding := fmt.Sprintf("%d audited request(s) since %s", len(recs), start.UTC().Format("2006-01-02"))
	if !until.IsZero() {
		finding += " until " + until.UTC().Format("2006-01-02 15:04Z")
	}
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
	detail := map[string]any{"total_matching": len(recs), "read_from_forward": len(all), "since": start.UTC().Format(time.RFC3339), "route_prefix": prefix, "method": method,
		"until": nilIfEmpty(until.UTC().Format(time.RFC3339)), "by_user": topDeviceCountRowsNamed(byUser, 10, "requests"), "by_method": byMethod, "by_outcome": byClass, "by_day": sortedCounts(byDay, false), "by_route": topDeviceCountRowsNamed(byRoute, 15, "requests"), "offset": in.Offset, "requests": win}
	return result.Build(inspectAccessName, result.OK, finding, result.Deterministic, cx, result.Options{Limits: limits, Omitted: omitted, NextActions: []string{"inspect-access", "inspect-collection"},
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

// routeShape is a route without its query and with numeric ids and long hashes as {id}, so requests to the same endpoint group together; the network id is kept.
func routeShape(uri string) string {
	if i := strings.IndexByte(uri, '?'); i >= 0 {
		q := uri[i:]
		uri = uri[:i]
		if a := regexp.MustCompile(`action=([A-Za-z]+)`).FindStringSubmatch(q); a != nil {
			uri += "?action=" + a[1]
		}
	}
	parts := strings.Split(uri, "/")
	for i, p := range parts {
		if i >= 3 && (idRe.MatchString(p) || len(p) > 24) {
			parts[i] = "{id}"
		}
	}
	return strings.Join(parts, "/")
}

var idRe = regexp.MustCompile(`^[0-9a-f-]{8,}$|^\d+$`)

// sortedCounts is a count map as rows in key order (byCount false) or biggest first.
func sortedCounts(m map[string]int, byCount bool) []map[string]any {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if byCount && m[keys[i]] != m[keys[j]] {
			return m[keys[i]] > m[keys[j]]
		}
		return keys[i] < keys[j]
	})
	out := make([]map[string]any, 0, len(keys))
	for _, k := range keys {
		out = append(out, map[string]any{"name": k, "requests": m[k]})
	}
	return out
}
