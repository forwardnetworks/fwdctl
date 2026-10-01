package skills

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	forward "github.com/forwardnetworks/forward-go-sdk"

	"github.com/forwardnetworks/fwdctl/fwd"
)

// resolveSnapshot returns the named snapshot, or the newest processed, non-predicted one. nil means there
// is nothing to read.
func resolveSnapshot(ctx context.Context, s *fwd.Session, networkID, snapshotID string) (*forward.Snapshot, error) {
	if snapshotID != "" {
		return s.Snapshot(ctx, networkID, snapshotID)
	}
	return s.LatestProcessed(ctx, networkID)
}

// window pages rows: the slice [offset, offset+limit) and the limit lines that tell the caller how to see the rest. ok is false
// when offset is past the end.
func window[T any](rows []T, limit, offset, dflt, max int, noun string) (out []T, limits []string, ok bool) {
	if limit <= 0 {
		limit = dflt
	}
	limit = min(limit, max)
	if offset >= len(rows) {
		return nil, nil, false
	}
	end := min(offset+limit, len(rows))
	if end < len(rows) {
		limits = append(limits, fmt.Sprintf("%d %s in all; rows %d-%d shown. Page with offset=%d.", len(rows), noun, offset+1, end, end))
	}
	return rows[offset:end], limits, true
}

// rejectForeignInputs is the validation of a skill whose inputs belong to views (a view or kind input picks one). viewInputs lists, per
// view, the inputs only that view takes; an input listed for some view but not the one asked for is rejected by name, so a caller who
// mixes views gets an error instead of an input that is silently ignored. Inputs no view lists (network_id, view itself) are shared.
func rejectForeignInputs(raw json.RawMessage, skill, discriminator, view string, viewInputs map[string][]string) error {
	var present map[string]json.RawMessage
	if err := json.Unmarshal(raw, &present); err != nil {
		return nil // the schema reports a non-object
	}
	own := map[string]bool{}
	for _, k := range viewInputs[view] {
		own[k] = true
	}
	var bad []string
	for v, keys := range viewInputs {
		if v == view {
			continue
		}
		for _, k := range keys {
			if _, ok := present[k]; ok && !own[k] {
				bad = append(bad, fmt.Sprintf("%s (an input of %s %s)", k, discriminator, v))
			}
		}
	}
	if len(bad) == 0 {
		return nil
	}
	sort.Strings(bad)
	return fmt.Errorf("%w: %s %s %s does not take %s", ErrInvalidInput, skill, discriminator, view, strings.Join(bad, ", "))
}
