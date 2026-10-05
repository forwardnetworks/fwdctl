package skills

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	forward "github.com/forwardnetworks/forward-go-sdk"

	"github.com/forwardnetworks/fwdctl/fwd"
	"github.com/forwardnetworks/fwdctl/result"
)

// resolveSnapshot returns the named snapshot, or the newest processed, non-predicted one. nil means there
// is nothing to read.
func resolveSnapshot(ctx context.Context, s *fwd.Session, networkID, snapshotID string) (*forward.Snapshot, error) {
	if snapshotID != "" {
		return s.Snapshot(ctx, networkID, snapshotID)
	}
	return s.LatestProcessed(ctx, networkID)
}

// window pages rows: the slice [offset, offset+limit) and the Omission that tells the caller what was left out and how to see the rest (nil when the page holds everything). ok is false
// when offset is past the end. The caller puts the omission in Options.Omitted.
func window[T any](rows []T, limit, offset, dflt, max int, noun string) (out []T, omitted []result.Omission, ok bool) {
	if limit <= 0 {
		limit = dflt
	}
	limit = min(limit, max)
	out, omitted, ok = result.Page(rows, offset, limit, noun)
	return out, omitted, ok
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

// pageNext is the "how to see the rest" of a page that ends at end of total: empty on the last page.
func pageNext(end, total int) string {
	if end >= total {
		return ""
	}
	return fmt.Sprintf("Page with offset=%d", end)
}

// joinNext joins the non-empty parts of a "how to see the rest" note.
func joinNext(parts ...string) string {
	var out []string
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, "; ")
}
