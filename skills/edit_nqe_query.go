package skills

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/forwardnetworks/fwdctl/fwd"
	"github.com/forwardnetworks/fwdctl/nqelint"
	"github.com/forwardnetworks/fwdctl/result"
)

const editNQEQueryName = "edit-nqe-query"

func init() { Register(editNQEQueryName, editNQEQuery) }

const maxQuerySource = 200_000

type editNQEQueryInput struct {
	Path    string `json:"path"`
	Source  string `json:"source"`
	Message string `json:"message"`
	Delete  bool   `json:"delete"`
	// CreateDirectory creates the enclosing directories that do not exist yet, in the same commit as the query.
	CreateDirectory bool `json:"create_directory"`
	Apply           bool `json:"apply"`
	// Changes replaces path/source/delete: several queries committed together in one commit (modules that import each other). message is the commit title.
	Changes []nqeChange `json:"changes"`
	// BasisCommitID is the library head the edits were made against; the plan and the apply refuse if the head is another commit.
	BasisCommitID string `json:"basis_commit_id"`
	// Typecheck stages the changes as drafts, has Forward type them and every importer, and restores the drafts.
	Typecheck  bool   `json:"typecheck"`
	SnapshotID string `json:"snapshot_id"`
	// DiscardDraft drops the caller's own uncommitted draft at exactly path (nothing else; nothing committed changes).
	DiscardDraft bool `json:"discard_draft"`
	// NetworkID is accepted so this skill takes the inputs find-nqe-query does; the library is organization-wide and it is not used.
	NetworkID string `json:"network_id"`
}

// missingParents lists the enclosing directories of path (root excluded, parents first) that no committed query lives under. A library directory exists
// only while a committed query is under it, so these do not exist.
func missingParents(path string, known map[string]int) []string {
	var out []string
	for i := 1; i < len(path); i++ {
		if path[i] == '/' {
			if d := path[:i+1]; known[d] == 0 {
				out = append(out, d)
			}
		}
	}
	return out
}

// editNQEQuery saves a query to the organization's NQE library (or removes one) and commits it, so it can be run by path or id. It
// refuses source that does not pass the offline check, reads what is there first so the dry run and the undo are exact, and reads the
// library back after writing to prove the commit holds what was sent.
func editNQEQuery(ctx context.Context, s *fwd.Session, raw json.RawMessage) (result.Result, error) {
	var in editNQEQueryInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return result.Result{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	if len(in.Changes) > 0 {
		return editNQEQueries(ctx, s, in)
	}
	if in.BasisCommitID != "" || in.Typecheck || in.SnapshotID != "" {
		return result.Result{}, fmt.Errorf("%w: basis_commit_id, typecheck and snapshot_id belong to changes (a multi-query commit)", ErrInvalidInput)
	}
	in.Path = strings.TrimSpace(in.Path)
	if in.Path == "" || !strings.HasPrefix(in.Path, "/") || strings.Contains(in.Path, "..") || len(in.Path) > 300 || strings.HasSuffix(in.Path, "/") {
		return result.Result{}, fmt.Errorf("%w: path is required and is the query's path in the library, starting with / (for example /Team/BGP neighbors down); a query at the root is /Name", ErrInvalidInput)
	}
	if in.DiscardDraft {
		if in.Delete || in.CreateDirectory || in.Source != "" {
			return result.Result{}, fmt.Errorf("%w: discard_draft takes only path (and apply)", ErrInvalidInput)
		}
		return discardNQEDraft(ctx, s, in)
	}
	if in.Delete && in.CreateDirectory {
		return result.Result{}, fmt.Errorf("%w: create_directory applies to saving a query", ErrInvalidInput)
	}
	if in.Delete && in.Source != "" {
		return result.Result{}, fmt.Errorf("%w: delete takes no source", ErrInvalidInput)
	}
	if !in.Delete && strings.TrimSpace(in.Source) == "" {
		return result.Result{}, fmt.Errorf("%w: source is required (or set delete to remove the query)", ErrInvalidInput)
	}
	if len(in.Source) > maxQuerySource {
		return result.Result{}, fmt.Errorf("%w: source is %d bytes; at most %d", ErrInvalidInput, len(in.Source), maxQuerySource)
	}
	cx := result.Context{Scope: "account", State: "current"}
	mode := result.ModeDryRun
	if in.Apply {
		mode = result.ModeApplied
	}
	next := []string{"validate-nqe-query", "find-nqe-query"}
	if !in.Delete {
		var errs []map[string]any
		for _, d := range nqelint.Lint(in.Source) {
			if d.Severity == "error" {
				errs = append(errs, map[string]any{"line": d.Line, "column": d.Column, "message": d.Message})
			}
		}
		if len(errs) > 0 {
			return result.Build(editNQEQueryName, result.Failed, fmt.Sprintf("The query does not pass the offline check (%d error(s)); it was not saved", len(errs)),
				result.Deterministic, cx, result.Options{Mode: mode, NextActions: []string{"author-nqe-query"},
					Evidence: []result.Evidence{result.NewEvidence(result.EvState, "nqelint", nil, map[string]any{"path": in.Path, "errors": errs}, "")},
					Limits:   []string{"nothing was changed; fix the errors (fwdctl nqe lint shows them with positions) and run again"}})
		}
	}
	prior, err := s.OrgQuery(ctx, in.Path)
	if err != nil {
		return result.Result{}, fmt.Errorf("reading the library failed, nothing was changed: %w", err)
	}
	priorSrc := ""
	if prior != nil {
		priorSrc = prior.Source
	}
	var newDirs []string
	if !in.Delete && prior == nil {
		known, err := s.OrgDirectoriesWithQueries(ctx)
		if err != nil {
			return result.Result{}, fmt.Errorf("reading the library failed, nothing was changed: %w", err)
		}
		newDirs = missingParents(in.Path, known)
		if len(newDirs) > 0 && !in.CreateDirectory {
			return result.Build(editNQEQueryName, result.Failed, fmt.Sprintf("The enclosing directory %s does not exist, so saving %s would be refused (409)", newDirs[len(newDirs)-1], in.Path), result.Deterministic, cx, result.Options{Mode: mode, NextActions: []string{"find-nqe-query"},
				Evidence: []result.Evidence{result.NewEvidence(result.EvState, "orgQuery", nil, map[string]any{"path": in.Path, "missing_directories": newDirs}, "")},
				Limits: []string{"the library holds no query under " + strings.Join(newDirs, ", ") + "; a library directory exists only while a committed query is in it, and a query must sit in an existing one (the root / always exists; Forward-owned roots such as /Security and /L3 and the organization's own folders do)",
					"to create the missing director" + map[bool]string{true: "ies", false: "y"}[len(newDirs) > 1] + " with the query run again with create_directory: true; to save at the root use /" + in.Path[strings.LastIndex(in.Path, "/")+1:] + "; nothing was changed"}})
		}
	}
	ev := func(extra map[string]any) []result.Evidence {
		d := map[string]any{"path": in.Path, "existed": prior != nil, "mode": mode}
		if len(newDirs) > 0 {
			d["creates_directories"] = newDirs
		}
		for k, v := range extra {
			d[k] = v
		}
		return []result.Evidence{result.NewEvidence(result.EvState, "orgQuery", nil, d, "")}
	}
	switch {
	case in.Delete && prior == nil:
		return result.Build(editNQEQueryName, result.OK, fmt.Sprintf("The library has no query at %s; nothing to delete", in.Path), result.Deterministic, cx,
			result.Options{Mode: mode, Evidence: ev(nil), NextActions: next})
	case !in.Delete && prior != nil && strings.TrimSpace(prior.Source) == strings.TrimSpace(in.Source):
		return result.Build(editNQEQueryName, result.OK, fmt.Sprintf("The query at %s already has that source; nothing to change", in.Path), result.Deterministic, cx,
			result.Options{Mode: mode, Evidence: ev(nil), NextActions: next})
	}
	action, undo := "save_query", ""
	switch {
	case in.Delete:
		action = "delete_query"
		undo = fmt.Sprintf("run edit-nqe-query again with path %q and the prior source (it is in this change's before) and apply=true", in.Path)
	case prior == nil:
		undo = fmt.Sprintf("run edit-nqe-query with path %q, delete=true and apply=true", in.Path)
	default:
		undo = fmt.Sprintf("run edit-nqe-query again with path %q and the prior source (this change's before) and apply=true", in.Path)
	}
	ch := result.Change{Action: action, Target: "library query " + in.Path, Before: priorSrc, After: in.Source, Reversible: true, Undo: undo}
	changes := []result.Change{ch}
	if len(newDirs) > 0 {
		// undo order: the query first, then each directory deepest first
		ch.Undo += "; the directories this change created go with the query (a library directory exists only while a committed query is in it)"
		changes = []result.Change{ch}
		for _, d := range newDirs {
			changes = append(changes, result.Change{Action: "create_directory", Target: "library directory " + d, Before: "", After: d, Reversible: true,
				Undo: "undone with the query: the directory is removed when its last query is deleted"})
		}
	}
	title := strings.TrimSpace(in.Message)
	if title == "" {
		title = map[bool]string{true: "Remove ", false: "Save "}[in.Delete] + in.Path
	}
	what := map[bool]string{true: "delete", false: map[bool]string{true: "replace", false: "add"}[prior != nil]}[in.Delete]
	if !in.Apply {
		return result.Build(editNQEQueryName, result.OK, fmt.Sprintf("Dry run: would %s the library query at %s and commit it. Nothing was changed; run again with apply=true to make it", what, in.Path),
			result.Deterministic, cx, result.Options{Mode: result.ModeDryRun, Changes: changes, Evidence: ev(nil), NextActions: next,
				Limits: []string{"the commit is visible to everyone in the organization; the offline check proves the query parses and type-checks, not that it returns what you want (validate-nqe-query runs it)"}})
	}
	if in.Delete {
		err = s.DeleteOrgQuery(ctx, in.Path, title, "")
	} else if len(newDirs) > 0 {
		err = s.SaveOrgQueryInNewDirectories(ctx, newDirs, in.Path, in.Source, title, "")
	} else {
		err = s.SaveOrgQuery(ctx, in.Path, in.Source, title, "")
	}
	if err != nil {
		return result.Result{}, fmt.Errorf("the %s failed: %w", what, err)
	}
	for i := range changes {
		changes[i].Applied = true
	}
	ch.Applied = true
	now, err := s.OrgQuery(ctx, in.Path)
	if err != nil {
		return result.Result{}, fmt.Errorf("the library accepted the commit but reading it back failed, so it is not proven: %w", err)
	}
	held := (in.Delete && now == nil) || (!in.Delete && now != nil && strings.TrimSpace(now.Source) == strings.TrimSpace(in.Source))
	if !held {
		return result.Build(editNQEQueryName, result.Failed, fmt.Sprintf("The commit was accepted but the library at %s does not hold the requested state", in.Path), result.Deterministic, cx,
			result.Options{Mode: result.ModeApplied, Changes: changes, Evidence: ev(map[string]any{"held": false}),
				Limits: []string{"read the library to see what it holds; the change may need undoing"}})
	}
	queryID := ""
	if now != nil {
		queryID = now.QueryID
	}
	return result.Build(editNQEQueryName, result.OK, fmt.Sprintf("Committed: %s the library query at %s", what, in.Path), result.Deterministic, cx,
		result.Options{Mode: result.ModeApplied, Changes: changes, Evidence: ev(map[string]any{"held": true, "query_id": queryID}), NextActions: next})
}

func dirEv(d map[string]any) []result.Evidence {
	return []result.Evidence{result.NewEvidence(result.EvState, "orgQuery", nil, d, "")}
}

// discardNQEDraft drops the login's own uncommitted draft at one exact path, which is how a stray draft left in the NQE editor is removed. It never touches what is committed
// and never discards more than that one path. The draft's source is recorded in before so the discard can be undone by saving it again.
func discardNQEDraft(ctx context.Context, s *fwd.Session, in editNQEQueryInput) (result.Result, error) {
	cx := result.Context{Scope: "account", State: "current"}
	mode := result.ModeDryRun
	if in.Apply {
		mode = result.ModeApplied
	}
	d, err := s.DraftAt(ctx, in.Path)
	if err != nil {
		return result.Result{}, err
	}
	if d == nil {
		return result.Build(editNQEQueryName, result.OK, fmt.Sprintf("You have no uncommitted draft at %s; nothing to discard", in.Path), result.Deterministic, cx,
			result.Options{Mode: mode, Limits: []string{"the NQE workspace of this login was read (ListDrafts); a path with no draft is left as it is"},
				Evidence: []result.Evidence{result.NewEvidence(result.EvState, "nqeDrafts", nil, map[string]any{"path": in.Path, "draft": nil}, "")}})
	}
	before := map[string]any{"draft": d.Type, "path": in.Path}
	if d.Source != "" {
		before["source"] = d.Source
	}
	ch := result.Change{Action: "discard_draft", Target: "uncommitted " + strings.ToLower(strings.TrimPrefix(d.Type, "QUERY_")) + " at " + in.Path, Before: before, Reversible: d.Source != "",
		Undo: "save the source in before again with edit-nqe-query (path and source)"}
	if d.Source == "" {
		ch.Undo = "none: Forward returned no source for the draft, so it cannot be restored"
	}
	limits := []string{"only your own uncommitted draft at exactly this path is dropped; what is committed in the library is not touched, and no other path is discarded",
		"the draft may be someone else's work if the login is shared (a team login): check before applying"}
	ev := []result.Evidence{result.NewEvidence(result.EvState, "nqeDrafts", nil, map[string]any{"path": in.Path, "draft": d.Type}, "")}
	if !in.Apply {
		return result.Build(editNQEQueryName, result.OK, fmt.Sprintf("Dry run: would discard your uncommitted %s at %s. Nothing was changed; run again with apply=true", strings.ToLower(strings.TrimPrefix(d.Type, "QUERY_")), in.Path),
			result.Deterministic, cx, result.Options{Mode: result.ModeDryRun, Changes: []result.Change{ch}, Limits: limits, Evidence: ev})
	}
	if err := s.DiscardOrgDraft(ctx, in.Path); err != nil {
		return result.Result{}, fmt.Errorf("the discard failed, nothing is known to have changed: %w", err)
	}
	ch.Applied = true
	if left, rerr := s.DraftAt(ctx, in.Path); rerr != nil {
		return result.Result{}, fmt.Errorf("the discard was sent but reading your drafts back failed, so it is not proven: %w", rerr)
	} else if left != nil {
		return result.Build(editNQEQueryName, result.Failed, fmt.Sprintf("Forward accepted the discard but a draft is still listed at %s", in.Path), result.Deterministic, cx,
			result.Options{Mode: result.ModeApplied, Changes: []result.Change{ch}, Limits: limits, Evidence: ev})
	}
	return result.Build(editNQEQueryName, result.OK, fmt.Sprintf("Discarded your uncommitted %s at %s (read back: gone)", strings.ToLower(strings.TrimPrefix(d.Type, "QUERY_")), in.Path),
		result.Deterministic, cx, result.Options{Mode: result.ModeApplied, Changes: []result.Change{ch}, Limits: limits, Evidence: ev})
}
