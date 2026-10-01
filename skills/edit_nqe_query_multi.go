package skills

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/forwardnetworks/fwdctl/fwd"
	"github.com/forwardnetworks/fwdctl/nqelint"
	"github.com/forwardnetworks/fwdctl/result"
)

const maxQueryChanges = 25

type nqeChange struct {
	Path   string `json:"path"`
	Source string `json:"source"`
}

// editNQEQueries changes several library queries in ONE commit (modules that import each other change together). The dry run lints every changed
// source offline and compares it with what is committed; with typecheck it also stages the changes as drafts and has Forward type every changed query
// and every query that imports one, then restores the drafts. Apply refuses if the library head moved since basis_commit_id (or since the plan was read),
// commits all paths together, reads each back, and returns the previous and the new commit id.
func editNQEQueries(ctx context.Context, s *fwd.Session, in editNQEQueryInput) (result.Result, error) {
	cx := result.Context{Scope: "account", State: "current"}
	mode := result.ModeDryRun
	if in.Apply {
		mode = result.ModeApplied
	}
	if in.Path != "" || in.Source != "" || in.Delete || in.CreateDirectory {
		return result.Result{}, fmt.Errorf("%w: changes replaces path, source, delete and create_directory; give one or the other", ErrInvalidInput)
	}
	if len(in.Changes) > maxQueryChanges {
		return result.Result{}, fmt.Errorf("%w: at most %d changed queries per commit", ErrInvalidInput, maxQueryChanges)
	}
	seen := map[string]bool{}
	for i := range in.Changes {
		c := &in.Changes[i]
		c.Path = strings.TrimSpace(c.Path)
		if c.Path == "" || !strings.HasPrefix(c.Path, "/") || strings.Contains(c.Path, "..") || strings.HasSuffix(c.Path, "/") || len(c.Path) > 300 {
			return result.Result{}, fmt.Errorf("%w: every change needs a path in the library starting with / (for example /Team/Module)", ErrInvalidInput)
		}
		if strings.TrimSpace(c.Source) == "" || len(c.Source) > maxQuerySource {
			return result.Result{}, fmt.Errorf("%w: every change needs a source of at most %d bytes (this skill does not delete)", ErrInvalidInput, maxQuerySource)
		}
		if seen[c.Path] {
			return result.Result{}, fmt.Errorf("%w: %s appears twice", ErrInvalidInput, c.Path)
		}
		seen[c.Path] = true
	}
	fail := func(msg string, detail map[string]any, limits ...string) (result.Result, error) {
		return result.Build(editNQEQueryName, result.Failed, msg, result.Deterministic, cx, result.Options{Mode: mode, Limits: append(limits, "nothing was committed"),
			Evidence: []result.Evidence{result.NewEvidence(result.EvState, "orgQueries", nil, detail, "")}})
	}

	// offline check of every source
	lintErrs := map[string][]map[string]any{}
	for _, c := range in.Changes {
		for _, d := range nqelint.Lint(c.Source) {
			if d.Severity == "error" {
				lintErrs[c.Path] = append(lintErrs[c.Path], map[string]any{"line": d.Line, "column": d.Column, "message": d.Message})
			}
		}
	}
	if len(lintErrs) > 0 {
		return fail(fmt.Sprintf("%d of %d query source(s) fail the offline check; nothing was changed", len(lintErrs), len(in.Changes)), map[string]any{"offline_errors": lintErrs},
			"fwdctl nqe lint shows the errors with positions. The offline check is gradual: it can miss shapes Forward rejects (for example a pattern capture's type), so typecheck: true asks Forward")
	}

	head, err := s.NQEHead(ctx)
	if err != nil {
		return result.Result{}, fmt.Errorf("reading the library head failed, nothing was changed: %w", err)
	}
	if in.BasisCommitID != "" && in.BasisCommitID != head {
		return fail(fmt.Sprintf("The library head is %s, not the basis %s you gave: someone committed since; nothing was changed", head, in.BasisCommitID),
			map[string]any{"head_commit_id": head, "basis_commit_id": in.BasisCommitID}, "re-read the queries at the new head (find-nqe-query), re-apply your edits on them, and run again")
	}
	basis := head

	type prev struct {
		src    string
		exists bool
	}
	prior := map[string]prev{}
	var changes []result.Change
	var unchanged, paths []string
	for _, c := range in.Changes {
		q, err := s.OrgQueryAt(ctx, c.Path, basis)
		if err != nil {
			return result.Result{}, fmt.Errorf("reading %s failed, nothing was changed: %w", c.Path, err)
		}
		p := prev{}
		if q != nil {
			p = prev{q.Source, true}
		}
		prior[c.Path] = p
		if p.exists && strings.TrimSpace(p.src) == strings.TrimSpace(c.Source) {
			unchanged = append(unchanged, c.Path)
			continue
		}
		action, undo := "edit_query", fmt.Sprintf("commit %s again with the prior source (this change's before)", c.Path)
		if !p.exists {
			action, undo = "add_query", fmt.Sprintf("delete %s with edit-nqe-query delete=true", c.Path)
		}
		changes = append(changes, result.Change{Action: action, Target: "library query " + c.Path, Before: p.src, After: c.Source, Reversible: true, Undo: undo})
		paths = append(paths, c.Path)
	}
	evd := func(extra map[string]any) []result.Evidence {
		d := map[string]any{"head_commit_id": head, "paths": paths, "unchanged": unchanged, "mode": mode}
		for k, v := range extra {
			d[k] = v
		}
		return []result.Evidence{result.NewEvidence(result.EvState, "orgQueries", nil, d, "")}
	}
	if len(paths) == 0 {
		return result.Build(editNQEQueryName, result.OK, "Every query already has the given source; nothing to commit", result.Deterministic, cx, result.Options{Mode: mode, Evidence: evd(nil)})
	}

	limits := []string{"the commit is visible to everyone in the organization and is ONE commit for all paths; Forward has no optimistic-concurrency check, so this skill compares the head to the basis before it commits (a commit landing in between is not caught)"}
	var tc map[string]any
	if in.Typecheck {
		restore := func() error {
			var errs []error
			for _, pth := range paths {
				p := prior[pth]
				if p.exists {
					if err := s.StageOrgQuery(ctx, pth, p.src); err != nil {
						errs = append(errs, fmt.Errorf("%s: %w", pth, err))
					}
				}
			}
			return errors.Join(errs...)
		}
		for _, c := range in.Changes {
			if err := s.StageOrgQuery(ctx, c.Path, c.Source); err != nil {
				_ = restore()
				return result.Result{}, fmt.Errorf("staging %s failed: %w", c.Path, err)
			}
		}
		dr, derr := s.NQECommitDryRun(ctx, paths, in.SnapshotID)
		if rerr := restore(); rerr != nil {
			limits = append(limits, "the staged drafts could not all be restored ("+rerr.Error()+"): check the NQE editor for uncommitted changes of yours at these paths")
		}
		if derr != nil {
			return result.Result{}, fmt.Errorf("Forward's typecheck failed: %w", derr)
		}
		errs := map[string][]map[string]any{}
		n := 0
		for pth, ds := range dr.NewErrors {
			for _, d := range ds {
				errs[pth] = append(errs[pth], map[string]any{"severity": d.Severity, "message": d.Message})
				n++
			}
		}
		tc = map[string]any{"new_errors": errs, "new_error_count": n, "uses_count": len(dr.Uses), "unauthorized": append(append([]string{}, dr.UnauthorizedQueryChanges...), dr.UnauthorizedAccessSettingChanges...)}
		limits = append(limits, "typecheck staged your changes as drafts in your workspace and restored them (Forward has no discard); new_errors include queries that import the changed ones; uses_count counts the checks and dashboards that consume them")
		if len(dr.UnauthorizedQueryChanges)+len(dr.UnauthorizedAccessSettingChanges) > 0 {
			return fail("Forward says this login may not commit some of these changes; nothing was changed", map[string]any{"typecheck": tc, "head_commit_id": head})
		}
		if n > 0 {
			return fail(fmt.Sprintf("Forward's typecheck finds %d new error(s) across %d quer(ies) (including importers); nothing was committed", n, len(errs)),
				map[string]any{"typecheck": tc, "head_commit_id": head, "paths": paths})
		}
	} else {
		limits = append(limits, "dependents were NOT typechecked: Forward types only staged changes, so set typecheck: true (it stages drafts, asks Forward, restores them) to see errors in the queries that import these")
	}
	title := strings.TrimSpace(in.Message)
	if title == "" {
		title = fmt.Sprintf("Update %d queries", len(paths))
	}
	if !in.Apply {
		return result.Build(editNQEQueryName, result.OK, fmt.Sprintf("Dry run: would commit %d quer(ies) in one commit on head %s. Nothing was committed; run again with apply=true to commit", len(paths), head),
			result.Deterministic, cx, result.Options{Mode: result.ModeDryRun, Changes: changes, Limits: limits, Evidence: evd(map[string]any{"typecheck": tc, "basis_commit_id": basis, "typechecked": in.Typecheck}),
				NextActions: []string{"validate-nqe-query", "find-nqe-query"}})
	}

	// apply: re-check the head, stage all, commit once, read back
	if h2, err := s.NQEHead(ctx); err != nil || h2 != basis {
		return fail(fmt.Sprintf("The library head moved from %s while planning (now %s); nothing was committed", basis, h2), map[string]any{"head_commit_id": h2, "basis_commit_id": basis})
	}
	for _, c := range in.Changes {
		if err := s.StageOrgQuery(ctx, c.Path, c.Source); err != nil {
			return result.Result{}, fmt.Errorf("staging %s failed, nothing was committed (earlier drafts may remain in your workspace): %w", c.Path, err)
		}
	}
	newHead, err := s.CommitOrgPaths(ctx, paths, title, "")
	if err != nil {
		for _, pth := range paths {
			if p := prior[pth]; p.exists {
				_ = s.StageOrgQuery(ctx, pth, p.src)
			}
		}
		return result.Result{}, fmt.Errorf("the commit failed, nothing was committed (drafts restored where possible): %w", err)
	}
	for i := range changes {
		changes[i].Applied = true
	}
	held := true
	var bad []string
	for _, c := range in.Changes {
		if q, err := s.OrgQueryAt(ctx, c.Path, newHead); err != nil || q == nil || strings.TrimSpace(q.Source) != strings.TrimSpace(c.Source) {
			held = false
			bad = append(bad, c.Path)
		}
	}
	sort.Strings(bad)
	ev := evd(map[string]any{"previous_commit_id": basis, "commit_id": newHead, "held": held, "typecheck": tc})
	if !held {
		return result.Build(editNQEQueryName, result.Failed, fmt.Sprintf("Committed %s but the library does not hold the requested source at: %s", newHead, strings.Join(bad, ", ")), result.Deterministic, cx,
			result.Options{Mode: result.ModeApplied, Changes: changes, Limits: limits, Evidence: ev})
	}
	return result.Build(editNQEQueryName, result.OK, fmt.Sprintf("Committed %d quer(ies) as one commit %s (previous head %s)", len(paths), newHead, basis), result.Deterministic, cx,
		result.Options{Mode: result.ModeApplied, Changes: changes, Limits: limits, Evidence: ev, NextActions: []string{"validate-nqe-query", "find-nqe-query"}})
}
