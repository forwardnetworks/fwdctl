package skills

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	forward "github.com/forwardnetworks/forward-go-sdk"

	"github.com/forwardnetworks/fwdctl/fwd"
	"github.com/forwardnetworks/fwdctl/result"
)

const (
	maxBulkDevices = 10000
	bulkChunk      = 2000
)

// bulkWorkers is how many devices are patched at once. Retries belong to the session's SDK client: the calls below are marked idempotent, so a write cut off mid-flight is repeated too.
var bulkWorkers = 8

func hasBulkDevices(raw json.RawMessage) bool {
	var keys map[string]json.RawMessage
	if json.Unmarshal(raw, &keys) != nil {
		return false
	}
	_, ok := keys["devices"]
	return ok
}

type bulkDeviceEntry struct {
	classicDeviceDef
	name string
}

// planDevicesBulk changes many classic devices in one call: definition.devices is a list of {name, ...fields} (the fields of a single update). The dry run shows each device's change (only the fields that change)
// and a count by target collector; the apply patches them a few at a time, retries a transient failure (the SDK client does), and reports each device. A name that is not in the list is skipped and named.
func planDevicesBulk(ctx context.Context, s *fwd.Session, in editSourceInput, cur []forward.ClassicDevice) (*networkPlan, error) {
	if in.Name != "" {
		return nil, fmt.Errorf("%w: give name (one device) or definition.devices (many), not both", ErrInvalidInput)
	}
	var def struct {
		Devices []json.RawMessage `json:"devices"`
	}
	var only map[string]json.RawMessage
	_ = json.Unmarshal(in.Definition, &only)
	if len(only) != 1 {
		return nil, fmt.Errorf("%w: with devices, give nothing else in definition (each entry carries its own fields)", ErrInvalidInput)
	}
	if err := json.Unmarshal(in.Definition, &def); err != nil || len(def.Devices) == 0 {
		return nil, fmt.Errorf("%w: definition.devices must be a non-empty list of {name, ...fields}", ErrInvalidInput)
	}
	if len(def.Devices) > maxBulkDevices {
		return nil, fmt.Errorf("%w: %d devices in one call; at most %d (split the list)", ErrInvalidInput, len(def.Devices), maxBulkDevices)
	}
	have := map[string]*forward.ClassicDevice{}
	for i := range cur {
		have[cur[i].Name] = &cur[i]
	}
	type item struct {
		name    string
		patch   forward.ClassicDevicePatch
		before  map[string]any
		after   map[string]any
		changes bool
		key     string
	}
	var items []item
	var missing []string
	seen := map[string]bool{}
	byCollector := map[string]int{}
	unchanged := 0
	for i, raw := range def.Devices {
		var e classicDeviceDef
		if err := decodeDefinition(raw, &e, "name plus the fields of a single device update"); err != nil {
			return nil, fmt.Errorf("devices[%d]: %w", i, err)
		}
		if strings.TrimSpace(e.Name) == "" {
			return nil, fmt.Errorf("%w: devices[%d] has no name", ErrInvalidInput, i)
		}
		if seen[e.Name] {
			return nil, fmt.Errorf("%w: device %q is listed twice", ErrInvalidInput, e.Name)
		}
		seen[e.Name] = true
		d, ok := have[e.Name]
		if !ok {
			missing = append(missing, e.Name)
			continue
		}
		patch, err := e.patchFor(d)
		if err != nil {
			return nil, fmt.Errorf("devices[%d] (%s): %w", i, e.Name, err)
		}
		after, _ := fwd.Generic(patch)
		am, _ := after.(map[string]any)
		if am == nil {
			am = map[string]any{}
		}
		if len(patch.Clear) > 0 {
			am["clear"] = patch.Clear
		}
		beforeFull, _ := fwd.Generic(d)
		bm, _ := beforeFull.(map[string]any)
		before := map[string]any{}
		same := true
		for k, v := range am {
			if k == "clear" {
				for _, c := range patch.Clear {
					before[c] = bm[c]
					if bm[c] != nil && fmt.Sprint(bm[c]) != "" {
						same = false
					}
				}
				continue
			}
			if fmt.Sprint(bm[k]) != fmt.Sprint(v) {
				same = false
			}
			before[k] = bm[k]
		}
		if len(am) == 0 || same {
			unchanged++
			continue
		}
		if c, ok := am["collectorId"].(string); ok {
			byCollector[c]++
		}
		kb, _ := json.Marshal(am)
		items = append(items, item{name: e.Name, patch: patch, before: before, after: am, changes: true, key: string(kb)})
	}
	if len(items) == 0 {
		return &networkPlan{target: "devices", noop: fmt.Sprintf("none of the %d listed devices changes (%d already as asked, %d not in the device list)", len(def.Devices), unchanged, len(missing))}, nil
	}
	// devices with the same change go in one request (one transaction on Forward: all or none), in chunks
	type batch struct {
		names []string
		patch forward.ClassicDevicePatch
	}
	var batches []batch
	groups := map[string][]int{}
	var order []string
	for i, it := range items {
		if _, ok := groups[it.key]; !ok {
			order = append(order, it.key)
		}
		groups[it.key] = append(groups[it.key], i)
	}
	for _, k := range order {
		idx := groups[k]
		for lo := 0; lo < len(idx); lo += bulkChunk {
			hi := min(lo+bulkChunk, len(idx))
			names := make([]string, 0, hi-lo)
			for _, i := range idx[lo:hi] {
				names = append(names, items[i].name)
			}
			batches = append(batches, batch{names, items[idx[lo]].patch})
		}
	}
	rows := make([]map[string]any, 0, len(items))
	for _, it := range items {
		rows = append(rows, map[string]any{"device": it.name, "before": it.before, "after": it.after})
	}
	shown, _ := result.CapRow(rows, 50)
	summary := map[string]any{"requests": len(batches), "devices_to_change": len(items), "already_as_asked": unchanged, "not_in_the_device_list": len(missing), "to_collector": byCollector, "first_changes": shown}
	if len(missing) > 0 {
		m, _ := result.CapRow(missing, 25)
		summary["not_found_names"] = m
	}
	limits := []string{
		fmt.Sprintf("this changes %d device(s) in %d request(s): devices with the same change share one request, which Forward applies as ONE transaction (all of its devices change or none: a bad credential, jump server or collector id fails that request), up to %d names to a request. Only the fields listed are sent (a partial update); a transient failure (a TLS or gateway timeout) is retried by the SDK client (up to 4 attempts with backoff, a write cut off mid-flight included: the same patch twice changes nothing more), which is safe because re-applying a patch changes nothing more; the first_changes list is the first 50 of the %d", len(items), len(batches), bulkChunk, len(items)),
		"a device whose name is not in the list is skipped and named (not_found_names); a device already as asked is left alone. Running it again re-applies only what is not yet as asked",
		"each change is a collection-source edit: it takes effect at the next collection",
	}
	var applyDetail map[string]any
	return &networkPlan{target: fmt.Sprintf("update %d devices on network %s", len(items), in.NetworkID), action: "update_devices", before: map[string]any{"devices": len(items)}, after: summary, reversible: true,
		undo: "update them again with the before values (the dry run lists them)", limits: limits,
		do: func(ctx context.Context) error {
			type outcome struct {
				n   int
				err error
			}
			results := make([]outcome, len(batches))
			var wg sync.WaitGroup
			ch := make(chan int)
			for w := 0; w < min(bulkWorkers, len(batches)); w++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					for i := range ch {
						// the same patch applied twice leaves the same devices, so the SDK may repeat it after a failure whose outcome is unknown
						_, err := s.Client.ClassicDevices.PatchMany(forward.WithIdempotent(ctx), in.NetworkID, batches[i].names, batches[i].patch)
						results[i] = outcome{len(batches[i].names), err}
					}
				}()
			}
			for i := range batches {
				ch <- i
			}
			close(ch)
			wg.Wait()
			var failed []string
			applied, failedDevices := 0, 0
			for i, r := range results {
				if r.err != nil {
					failedDevices += r.n
					note := oneLineNote(r.err.Error())
					if d, ok := forward.RateLimitLockout(r.err); ok {
						note += fmt.Sprintf(" (Forward's API rate limit locked this login out for another %s; every session sharing it is refused until then, so wait and run it again with fewer requests in a minute)", d.Round(time.Second))
					}
					failed = append(failed, fmt.Sprintf("request %d (%d devices, starting %s): %s", i+1, r.n, batches[i].names[0], note))
				} else {
					applied += r.n
				}
			}
			shownF, _ := result.CapRow(failed, 25)
			applyDetail = map[string]any{"devices_applied": applied, "devices_failed": failedDevices, "requests": len(batches), "failed_requests": nonNilStrings(shownF)}
			if failedDevices > 0 {
				return &partialApplyError{msg: fmt.Sprintf("%d of %d devices were NOT changed (their request failed after the SDK client's retries, so none of that request's devices changed); the other %d were applied. Run it again: it retries only the devices not yet as asked", failedDevices, len(items), applied), detail: applyDetail}
			}
			return nil
		},
		verify: func(ctx context.Context) (bool, any, error) {
			l, _, err := s.Client.ClassicDevices.List(ctx, in.NetworkID)
			if err != nil {
				return false, nil, err
			}
			now := map[string]map[string]any{}
			for _, d := range l {
				g, _ := fwd.Generic(d)
				if m, ok := g.(map[string]any); ok {
					now[d.Name] = m
				}
			}
			bad := []string{}
			for _, it := range items {
				for k, v := range it.after {
					if fmt.Sprint(now[it.name][k]) != fmt.Sprint(v) {
						bad = append(bad, it.name)
						break
					}
				}
			}
			out := map[string]any{"devices_checked": len(items), "not_as_asked": len(bad)}
			if applyDetail != nil {
				out["apply"] = applyDetail
			}
			if len(bad) > 0 {
				sh, _ := result.CapRow(bad, 25)
				out["not_as_asked_names"] = sh
			}
			return len(bad) == 0, out, nil
		}}, nil
}

// partialApplyError is returned by a write that applied some of its changes and failed others: the result is Failed, says what was applied, and carries the per-item detail, instead of claiming nothing changed.
type partialApplyError struct {
	msg    string
	detail map[string]any
}

func (e *partialApplyError) Error() string { return e.msg }
