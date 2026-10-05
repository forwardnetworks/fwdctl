package result

import (
	"fmt"
	"strings"
)

// Omission says that a list in the result was cut: what it is, how many items exist, how many are shown, and how to see the rest. It is part of the envelope (and rendered into limits
// by Build) so that a caller comparing two answers can tell a short list from a cut one without parsing prose, and so a skill cannot cut a list and stay silent.
type Omission struct {
	What  string `json:"what"`            // the thing counted, for example "devices" or "distinct paths"
	Total int    `json:"total"`           // how many exist in what was read
	Shown int    `json:"shown"`           // how many are in the result
	From  int    `json:"from,omitempty"`  // zero-based position of the first shown item when the list is paged
	Paged bool   `json:"paged,omitempty"` // true when the list is a page of an offset-paged read (the text then says which rows)
	Next  string `json:"next,omitempty"`  // how to see the rest, for example "page with offset=50" or "narrow with device"
}

// Text is the sentence Build adds to limits for an omission.
func (o Omission) Text() string {
	s := fmt.Sprintf("%d %s in all; %d shown", o.Total, o.What, o.Shown)
	if o.Paged {
		s = fmt.Sprintf("%d %s in all; rows %d-%d shown", o.Total, o.What, o.From+1, o.From+o.Shown)
	}
	if o.Next != "" {
		s += ". " + strings.TrimRight(o.Next, ".") + "."
	}
	return s
}

// Cap returns at most n items of items. When it cuts, it also returns the Omission to put in Options.Omitted, so the cut is reported wherever it happens. It is the sanctioned way to
// bound a list in a skill (TestRawListCutsAreAllowlisted).
func Cap[T any](items []T, n int, what, next string) ([]T, []Omission) {
	if n < 0 || len(items) <= n {
		return items, nil
	}
	return items[:n], []Omission{{What: what, Total: len(items), Shown: n, Next: next}}
}

// Page returns items[offset:offset+limit] (offset and limit already validated) and the Omission when the page does not hold the whole list. ok is false when offset is past the end.
func Page[T any](items []T, offset, limit int, what string) (page []T, omitted []Omission, ok bool) {
	if offset < 0 || offset >= len(items) {
		return nil, nil, false
	}
	end := min(offset+limit, len(items))
	page = items[offset:end]
	if offset > 0 || end < len(items) {
		next := ""
		if end < len(items) {
			next = fmt.Sprintf("Page with offset=%d", end)
		}
		omitted = []Omission{{What: what, Total: len(items), Shown: end - offset, From: offset, Paged: true, Next: next}}
	}
	return page, omitted, true
}

func (o Omission) validate() string {
	switch {
	case strings.TrimSpace(o.What) == "":
		return "what is required"
	case o.Shown < 0 || o.From < 0:
		return "shown and from must not be negative"
	case o.From+o.Shown > o.Total:
		return "shown cannot exceed total"
	case o.From == 0 && o.Shown >= o.Total:
		return "an omission that omits nothing is not an omission"
	}
	return ""
}

// CapRow bounds a list that sits inside one row of the result (the devices of one exception, say). It returns the shown items and the true total, which the row must carry beside
// the list (for example "devices_total"), so a reader of the row can tell a short list from a cut one. A list of the result's own rows uses Cap or Page instead.
func CapRow[T any](items []T, n int) (shown []T, total int) {
	if n < 0 || len(items) <= n {
		return items, len(items)
	}
	return items[:n], len(items)
}
