package skills

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/forwardnetworks/fwdctl/fwd"
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
	if in.Path != "" || in.Source != "" || in.Delete {
		return result.Result{}, fmt.Errorf("%w: changes replaces path, source and delete; give one or the other (create_directory does go with changes)", ErrInvalidInput)
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
	schemaUsed := "embedded"
	for _, c := range in.Changes {
		diags, used, lerr := lintWithSchema(ctx, s, in.OfflineCheck, c.Source)
		if lerr != nil {
			return result.Result{}, lerr
		}
		schemaUsed = used
		for _, d := range diags {
			if d.Severity == "error" {
				lintErrs[c.Path] = append(lintErrs[c.Path], map[string]any{"line": d.Line, "column": d.Column, "message": d.Message})
			}
		}
	}
	if len(lintErrs) > 0 {
		return fail(fmt.Sprintf("%d of %d query source(s) fail the offline check; nothing was changed", len(lintErrs), len(in.Changes)), map[string]any{"offline_errors": lintErrs},
			"offline check schema: "+schemaUsed+" (offline_check: org uses the organization's live schema, skip with typecheck: true lets Forward's own check decide)",
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
	// a NEW query needs its enclosing directories to exist; create_directory makes the missing ones in the same commit
	var newDirs []string
	{
		wanted := map[string]bool{}
		var known map[string]int
		for _, c := range in.Changes {
			if prior[c.Path].exists {
				continue
			}
			if known == nil {
				var kerr error
				if known, kerr = s.OrgDirectoriesWithQueries(ctx); kerr != nil {
					return result.Result{}, fmt.Errorf("reading the library failed, nothing was changed: %w", kerr)
				}
			}
			for _, d := range missingParents(c.Path, known) {
				wanted[d] = true
			}
		}
		for d := range wanted {
			newDirs = append(newDirs, d)
		}
		sort.Slice(newDirs, func(i, j int) bool { // parents first: a parent path is a prefix of (so shorter than) its child
			if len(newDirs[i]) != len(newDirs[j]) {
				return len(newDirs[i]) < len(newDirs[j])
			}
			return newDirs[i] < newDirs[j]
		})
	}
	if len(newDirs) > 0 && !in.CreateDirectory {
		return fail(fmt.Sprintf("%d enclosing director%s do not exist (%s), so saving the new queries would be refused (409); nothing was changed", len(newDirs), map[bool]string{true: "y does", false: "ies"}[len(newDirs) == 1], strings.Join(newDirs, ", ")),
			map[string]any{"head_commit_id": head, "missing_directories": newDirs},
			"a library directory exists only while a committed query is in it; run again with create_directory: true to create the missing ones in the same commit")
	}
	for _, d := range newDirs {
		changes = append(changes, result.Change{Action: "create_directory", Target: "library directory " + d, Before: "", After: d, Reversible: true,
			Undo: "undone with the queries: the directory is removed when its last query is deleted"})
	}
	discardAll := func() error {
		return errors.Join(s.DiscardOrgDrafts(ctx, paths), s.DiscardOrgDirectories(ctx, newDirs))
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

	limits := []string{}
	switch schemaUsed {
	case "skipped":
		limits = append(limits, "the offline check was skipped (offline_check: skip): Forward's own typecheck is the only gate; read its result below")
	case "organization's":
		limits = append(limits, "the offline check used the organization's live schema (offline_check: org)")
	}
	limits = append(limits, "the commit is visible to everyone in the organization and is ONE commit for all paths; Forward has no optimistic-concurrency check, so this skill compares the head to the basis before it commits (a commit landing in between is not caught)")
	var tc map[string]any
	if in.Typecheck {
		if mine, derr := s.DraftsAt(ctx, paths); derr != nil {
			return result.Result{}, fmt.Errorf("reading your uncommitted NQE changes failed, so nothing was staged: %w", derr)
		} else if len(mine) > 0 {
			return fail(fmt.Sprintf("you already have uncommitted changes in the NQE editor at %s; commit or discard them there first, because staging and cleaning up would overwrite or drop them. Nothing was changed", strings.Join(mine, ", ")),
				map[string]any{"head_commit_id": head, "drafts_at": mine})
		}
		discard := discardAll
		if err := s.AddOrgDirectories(ctx, newDirs); err != nil {
			return result.Result{}, fmt.Errorf("staging the missing directories failed, nothing is left in your workspace: %w", err)
		}
		for _, c := range in.Changes {
			if err := s.StageOrgQuery(ctx, c.Path, c.Source); err != nil {
				derr := discard()
				msg := fmt.Sprintf("staging %s failed: %v", c.Path, err)
				if derr != nil {
					msg += "; discarding the drafts failed too, check the NQE editor for uncommitted changes of yours: " + derr.Error()
				} else {
					msg += "; the drafts staged so far were discarded, nothing is left in your workspace"
				}
				return result.Result{}, errors.New(msg)
			}
		}
		dr, derr := s.NQECommitDryRun(ctx, paths, in.SnapshotID)
		if rerr := discard(); rerr != nil {
			limits = append(limits, "the staged drafts could not be discarded ("+rerr.Error()+"): check the NQE editor for uncommitted changes of yours at these paths")
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
		limits = append(limits, "typecheck staged your changes as drafts in your workspace, asked Forward, and discarded them; new_errors include queries that import the changed ones; uses_count counts the checks and dashboards that consume them")
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
	if mine, derr := s.DraftsAt(ctx, paths); derr != nil {
		return result.Result{}, fmt.Errorf("reading your uncommitted NQE changes failed, so nothing was staged: %w", derr)
	} else if len(mine) > 0 {
		return fail(fmt.Sprintf("you already have uncommitted changes in the NQE editor at %s; commit or discard them there first. Nothing was committed", strings.Join(mine, ", ")),
			map[string]any{"head_commit_id": head, "drafts_at": mine})
	}
	if err := s.AddOrgDirectories(ctx, newDirs); err != nil {
		return result.Result{}, fmt.Errorf("staging the missing directories failed, nothing was committed: %w", err)
	}
	for _, c := range in.Changes {
		if err := s.StageOrgQuery(ctx, c.Path, c.Source); err != nil {
			msg := fmt.Sprintf("staging %s failed, nothing was committed", c.Path)
			if derr := discardAll(); derr != nil {
				msg += "; the drafts staged so far could not be discarded, check the NQE editor: " + derr.Error()
			} else {
				msg += "; the drafts staged so far were discarded"
			}
			return result.Result{}, fmt.Errorf("%s: %w", msg, err)
		}
	}
	newHead, err := s.CommitOrgPaths(ctx, paths, title, "")
	if err != nil {
		msg := "the commit failed, nothing was committed"
		if derr := discardAll(); derr != nil {
			msg += "; the staged drafts could not be discarded, check the NQE editor: " + derr.Error()
		} else {
			msg += "; the staged drafts were discarded"
		}
		return result.Result{}, fmt.Errorf("%s: %w", msg, err)
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

// MaxQueryChanges is how many queries one commit may carry; `fwdctl nqe pack` refuses a larger tree with this number.
const MaxQueryChanges = maxQueryChanges
