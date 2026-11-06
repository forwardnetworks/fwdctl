package fwd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	forward "github.com/forwardnetworks/forward-go-sdk"
)

// ErrGuardRefused is the error a guard (Config.ReadOnly or Config.AllowNetworks) returns for a request it stops before it leaves the process.
var ErrGuardRefused = errors.New("refused by this session's guard")

// permanent is appended to every guard refusal so the SDK does not retry it (a refusal is not transient); it prints as nothing.
var permanent = quietErr{forward.ErrPermanent}

type quietErr struct{ err error }

func (q quietErr) Error() string { return "" }
func (q quietErr) Unwrap() error { return q.err }

// guardBypass marks the guard's own snapshot lookups, which must not be checked against the guard that sent them.
type guardBypass struct{}

// readOnlyGuard refuses PUT, PATCH and DELETE at the transport (Config.ReadOnly). A POST still goes through, because NQE, path searches and
// other reads are POSTs; skills.Run refuses a write skill with apply on a read-only session, which covers the writes that POST.
type readOnlyGuard struct{ next http.RoundTripper }

func (t readOnlyGuard) RoundTrip(r *http.Request) (*http.Response, error) {
	switch r.Method {
	case http.MethodPut, http.MethodPatch, http.MethodDelete:
		return nil, fmt.Errorf("%w: this session is read-only (FWDCTL_READONLY); %s %s was not sent%w", ErrGuardRefused, r.Method, r.URL.Path, permanent)
	}
	return t.next.RoundTrip(r)
}

// networkGuard is Config.AllowNetworks: every request must name only allowed networks, or snapshots of them, before it is sent. It is deny by
// default: a route that is not scoped to a network (users, organisations, integrations, collectors, the NQE library) is refused, except the few
// that hold no network data (the version, the current user, the NQE schema). The network list is answered with the allowed networks only.
type networkGuard struct {
	next    http.RoundTripper
	allowed map[string]bool
	sess    *Session

	mu     sync.Mutex
	snaps  map[string]string // snapshot id -> allowed network id, from the allowed networks' own lists
	loaded time.Time
	tried  map[string]bool // snapshot ids already looked for
}

func newNetworkGuard(next http.RoundTripper, ids []string, s *Session) *networkGuard {
	g := &networkGuard{next: next, allowed: map[string]bool{}, sess: s, snaps: map[string]string{}, tried: map[string]bool{}}
	for _, id := range ids {
		if id = strings.TrimSpace(id); id != "" {
			g.allowed[id] = true
		}
	}
	return g
}

func (g *networkGuard) list() string {
	ids := make([]string, 0, len(g.allowed))
	for id := range g.allowed {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return strings.Join(ids, ",")
}

func (g *networkGuard) refuse(r *http.Request, why string) error {
	return fmt.Errorf("%w: %s; this session may reach only networks %s (FWDCTL_ALLOW_NETWORKS), so %s %s was not sent%w", ErrGuardRefused, why, g.list(), r.Method, r.URL.Path, permanent)
}

func (g *networkGuard) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.Context().Value(guardBypass{}) != nil {
		return g.next.RoundTrip(r)
	}
	if r.URL.Opaque != "" || r.URL.RawQuery != "" && !validQuery(r.URL.RawQuery) {
		return nil, g.refuse(r, "a request URL the guard cannot read")
	}
	path := r.URL.EscapedPath()
	seg := strings.Split(strings.Trim(path, "/"), "/")
	for i := range seg {
		u, err := url.PathUnescape(seg[i])
		if err != nil || u == "" || slices.Contains(strings.Split(u, "/"), ".") || slices.Contains(strings.Split(u, "/"), "..") {
			return nil, g.refuse(r, "a request path the guard cannot read")
		}
		seg[i] = u
	}
	if len(seg) < 2 || seg[0] != "api" {
		return nil, g.refuse(r, "not a Forward API route")
	}
	rest := seg[1:]
	filterNetworks := false
	switch rest[0] {
	case "networks":
		if len(rest) == 1 {
			if r.Method != http.MethodGet {
				return nil, g.refuse(r, "creating a network is not scoped to an allowed network")
			}
			filterNetworks = true
			break
		}
		if !g.allowed[rest[1]] {
			return nil, g.refuse(r, "network "+rest[1]+" is not allowed")
		}
	case "snapshots":
		if len(rest) < 2 {
			return nil, g.refuse(r, "a snapshot route without a snapshot id")
		}
		if err := g.snapshotAllowed(r, rest[1]); err != nil {
			return nil, err
		}
	case "diffs", "nqe-diffs":
		if len(rest) < 3 {
			return nil, g.refuse(r, "a comparison without two snapshot ids")
		}
		for _, id := range rest[1:3] {
			if err := g.snapshotAllowed(r, id); err != nil {
				return nil, err
			}
		}
	case "nqe":
		switch {
		case len(rest) == 1: // a query run: its scope is in the query string, checked below, and must be there
			q := r.URL.Query()
			if q.Get("networkId") == "" && q.Get("snapshotId") == "" {
				return nil, g.refuse(r, "an NQE run without a network or snapshot")
			}
		case len(rest) == 2 && rest[1] == "schema" && r.Method == http.MethodGet:
		default:
			return nil, g.refuse(r, "this NQE route is not scoped to a network")
		}
	case "version":
		if r.Method != http.MethodGet {
			return nil, g.refuse(r, "only reading the version is allowed")
		}
	case "users":
		if len(rest) != 2 || rest[1] != "current" || r.Method != http.MethodGet {
			return nil, g.refuse(r, "user routes are not scoped to a network")
		}
	default:
		return nil, g.refuse(r, "/api/"+rest[0]+" is not scoped to a network")
	}
	for k, vs := range r.URL.Query() {
		for _, v := range vs {
			for _, id := range strings.Split(v, ",") { // a list in one parameter (ids=a,b) is checked item by item
				if err := g.idAllowed(r, k, strings.TrimSpace(id)); err != nil {
					return nil, err
				}
			}
		}
	}
	if r.Body != nil && r.Body != http.NoBody {
		ct := strings.ToLower(r.Header.Get("Content-Type"))
		// a JSON body (or one that says nothing of its type) is read and every id in it checked; an upload (multipart, zip, octet-stream) holds
		// no ids to check and is streamed as it is, so a large snapshot is never held in memory
		if !streamable(ct) {
			b, err := io.ReadAll(r.Body)
			_ = r.Body.Close()
			if err != nil {
				return nil, err
			}
			r.Body = io.NopCloser(bytes.NewReader(b))
			if err := g.bodyAllowed(r, b); err != nil {
				return nil, err
			}
		}
	}
	resp, err := g.next.RoundTrip(r)
	if err != nil || !filterNetworks || resp.StatusCode < 200 || resp.StatusCode > 299 {
		return resp, err
	}
	return g.filterNetworkList(resp)
}

// streamable says a body is an upload (multipart, zip, octet-stream) that holds no ids and is sent unread. Every other body, JSON or not, is read
// and must parse as JSON: a form body would be read by Forward as parameters.
func streamable(contentType string) bool {
	for _, t := range []string{"multipart/", "application/zip", "application/octet-stream", "application/gzip", "application/x-gzip"} {
		if strings.HasPrefix(contentType, t) {
			return true
		}
	}
	return false
}

func validQuery(raw string) bool {
	_, err := url.ParseQuery(raw)
	return err == nil
}

// IDKind says whether a parameter or JSON key names network ids or snapshot ids: any name ending in networkId(s) or snapshotId(s), in any case and
// with or without _ or - (networkId, targetNetworkId, network_ids, baseSnapshotId, targetBaseSnapshotId, snapshotIds), and the diff spellings
// snapshotAId and snapshotBId. Other keys hold no id the guard has to check.
func IDKind(key string) string {
	k := strings.NewReplacer("_", "", "-", "").Replace(strings.ToLower(key))
	switch {
	case strings.HasSuffix(k, "networkid"), strings.HasSuffix(k, "networkids"):
		return "network"
	case strings.HasSuffix(k, "snapshotid"), strings.HasSuffix(k, "snapshotids"), k == "snapshotaid", k == "snapshotbid":
		return "snapshot"
	}
	return ""
}

// idAllowed checks one value of a key idKind names.
func (g *networkGuard) idAllowed(r *http.Request, key, id string) error {
	if id == "" {
		return nil
	}
	switch IDKind(key) {
	case "network":
		if !g.allowed[id] {
			return g.refuse(r, key+" "+id+" is not an allowed network")
		}
	case "snapshot":
		return g.snapshotAllowed(r, id)
	}
	return nil
}

// bodyAllowed checks every id-naming key (IDKind) in a JSON body, at any depth, a single value or a list. A body that does not parse as JSON is
// refused: Forward's parser accepts what Go's rejects, so an unreadable body could still carry an id.
func (g *networkGuard) bodyAllowed(r *http.Request, b []byte) error {
	if len(bytes.TrimSpace(b)) == 0 {
		return nil
	}
	var v any
	if json.Unmarshal(b, &v) != nil {
		return g.refuse(r, "a request body the guard cannot read as JSON")
	}
	var walk func(any) error
	walk = func(v any) error {
		switch t := v.(type) {
		case map[string]any:
			for k, x := range t {
				if IDKind(k) != "" {
					ids := []any{x}
					if l, ok := x.([]any); ok {
						ids = l
					}
					for _, e := range ids {
						if id, ok := idString(e); ok {
							if err := g.idAllowed(r, k, id); err != nil {
								return err
							}
						}
					}
				}
				if err := walk(x); err != nil {
					return err
				}
			}
		case []any:
			for _, x := range t {
				if err := walk(x); err != nil {
					return err
				}
			}
		}
		return nil
	}
	return walk(v)
}

func idString(v any) (string, bool) {
	switch t := v.(type) {
	case string:
		return t, t != ""
	case float64:
		return fmt.Sprintf("%.0f", t), true
	}
	return "", false
}

// snapshotAllowed lets a snapshot through only when it is one of the allowed networks' own snapshots: their snapshot lists (archived included)
// and the predicted snapshots their change sets name. Forward is never asked about a snapshot outside that set, and a refusal does not say
// where it is. On a miss the lists are read again, at most once every few seconds, so a snapshot made during the run is found.
func (g *networkGuard) snapshotAllowed(r *http.Request, id string) error {
	g.mu.Lock()
	ok := g.snaps[id] != ""
	stale := time.Since(g.loaded) > snapshotReload || !g.tried[id] // an id not seen before always earns one reload, so a snapshot made a moment ago is found
	g.tried[id] = true
	g.mu.Unlock()
	if !ok && stale {
		g.load(context.WithValue(r.Context(), guardBypass{}, true))
		g.mu.Lock()
		ok = g.snaps[id] != ""
		g.mu.Unlock()
	}
	if !ok {
		return g.refuse(r, "snapshot "+id+" is not a snapshot of an allowed network")
	}
	return nil
}

// maxGuardChangeSets bounds the change sets whose full prediction lists are read per load.
const maxGuardChangeSets = 200

// snapshotReload is how soon the snapshot lists may be read again after a miss.
const snapshotReload = 5 * time.Second

// load reads every allowed network's snapshots and change sets' predicted snapshots through the SDK. A list that cannot be read adds nothing:
// the guard fails closed.
func (g *networkGuard) load(ctx context.Context) {
	if g.sess == nil || g.sess.Client == nil {
		return
	}
	found := map[string]string{}
	yes := true
	for net := range g.allowed {
		if l, _, err := g.sess.Client.Snapshots.List(ctx, net, forward.SnapshotListOptions{IncludeArchived: &yes}); err == nil {
			for _, sn := range l {
				found[sn.ID.String()] = net
			}
		}
		if cs, _, err := g.sess.Client.Predict.ListChangeSets(ctx, net); err == nil {
			for i, c := range cs {
				for _, p := range c.PredictedSnapshots {
					if n := p.NetworkID.String(); n == "" || n == net {
						found[p.ID.String()] = net
					}
				}
				// the change-set row names only recent predictions; the change set's own list has every one (bounded, so a large network costs a few hundred reads at most)
				if i < maxGuardChangeSets {
					if ps, _, perr := g.sess.Client.Predict.ListPredictedSnapshots(ctx, net, c.ID.String()); perr == nil {
						for _, p := range ps {
							found[p.ID.String()] = net
						}
					}
				}
			}
		}
	}
	g.mu.Lock()
	for k, v := range found {
		g.snaps[k] = v
	}
	g.loaded = time.Now()
	g.mu.Unlock()
}

// filterNetworkList answers GET /api/networks with the allowed networks only. A body it cannot read as a list of networks is refused rather than passed on.
func (g *networkGuard) filterNetworkList(resp *http.Response) (*http.Response, error) {
	b, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		return nil, err
	}
	var items []map[string]any
	if err := json.Unmarshal(b, &items); err != nil {
		return nil, fmt.Errorf("%w: the network list could not be read to keep only networks %s%w", ErrGuardRefused, g.list(), permanent)
	}
	kept := make([]map[string]any, 0, len(items))
	for _, it := range items {
		if id, ok := idString(it["id"]); ok && g.allowed[id] {
			kept = append(kept, it)
		}
	}
	out, _ := json.Marshal(kept)
	resp.Body = io.NopCloser(bytes.NewReader(out))
	resp.ContentLength = int64(len(out))
	resp.Header.Del("Content-Length")
	return resp, nil
}
