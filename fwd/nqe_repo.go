package fwd

import (
	"context"
	"errors"
	"sort"
	"strings"

	forward "github.com/forwardnetworks/forward-go-sdk"
)

// OrgQuery is a query of the organization's NQE library as committed at its head.
type OrgQuery struct {
	Path     string
	Source   string
	QueryID  string
	CommitID string // the library commit the source was read at
}

// OrgQuery reads one committed query by path, or nil when the library has none there.
func (s *Session) OrgQuery(ctx context.Context, path string) (*OrgQuery, error) {
	return s.OrgQueryAt(ctx, path, "")
}

// OrgQueryAt reads a saved query by path at a library commit (empty: the head).
func (s *Session) OrgQueryAt(ctx context.Context, path, commitID string) (*OrgQuery, error) {
	head, _, err := s.Client.NQERepository.Head(ctx)
	if err != nil {
		return nil, err
	}
	at := string(head)
	if commitID != "" {
		at = commitID
	}
	list, _, err := s.Client.NQERepository.ListHeadQueries(ctx)
	if err != nil {
		return nil, err
	}
	for _, q := range list {
		if strings.TrimSpace(q.Path) == path {
			got, _, err := s.Client.NQERepository.GetQuery(ctx, at, path)
			if err != nil {
				return nil, err
			}
			return &OrgQuery{Path: path, Source: got.SourceCode, QueryID: string(got.QueryID), CommitID: at}, nil
		}
	}
	return nil, nil
}

// SaveOrgQuery adds or replaces a query and commits it to the organization's library. When the staging or the commit fails the draft is discarded again so
// nothing half-finished stays in the workspace.
func (s *Session) SaveOrgQuery(ctx context.Context, path, source, title, body string) error {
	if mine, err := s.DraftsAt(ctx, []string{path}); err != nil {
		return err
	} else if len(mine) > 0 {
		return errors.New("you already have an uncommitted change at " + path + " in the NQE editor; commit or discard it there first (cleaning up here would drop it)")
	}
	if err := s.StageOrgQuery(ctx, path, source); err != nil {
		return errors.Join(err, s.discardAfter(ctx, path))
	}
	if _, err := s.Client.NQERepository.Commit(ctx, forward.NQECommitRequest{Paths: []string{path}, Message: &forward.NQECommitMessage{Title: title, Body: body}}); err != nil {
		return errors.Join(err, s.discardAfter(ctx, path))
	}
	return nil
}

// discardAfter drops the draft at path after a failure; its own failure is returned so the caller can say a draft may remain.
func (s *Session) discardAfter(ctx context.Context, paths ...string) error {
	if err := s.DiscardOrgDrafts(ctx, paths); err != nil {
		return errors.New("the uncommitted draft could not be discarded either (check the NQE editor): " + err.Error())
	}
	return nil
}

// DeleteOrgQuery removes a query from the organization's library and commits the removal.
func (s *Session) DeleteOrgQuery(ctx context.Context, path, title, body string) error {
	if mine, err := s.DraftsAt(ctx, []string{path}); err != nil {
		return err
	} else if len(mine) > 0 {
		return errors.New("you already have an uncommitted change at " + path + " in the NQE editor; commit or discard it there first (cleaning up here would drop it)")
	}
	if _, err := s.Client.NQERepository.DeleteQuery(ctx, path); err != nil {
		return err
	}
	if _, err := s.Client.NQERepository.Commit(ctx, forward.NQECommitRequest{Paths: []string{path}, Message: &forward.NQECommitMessage{Title: title, Body: body}}); err != nil {
		return errors.Join(err, s.discardAfter(ctx, path))
	}
	return nil
}

// OrgQueryByID reads a committed organization query's source by its stable id ("Q_..."), or nil when there is none.
func (s *Session) OrgQueryByID(ctx context.Context, queryID string) (*forward.NQEQuerySourceAtCommit, error) {
	return s.OrgQueryByIDAt(ctx, queryID, "")
}

// OrgQueryByIDAt reads a saved query's source at a library commit (empty: the head).
func (s *Session) OrgQueryByIDAt(ctx context.Context, queryID, commitID string) (*forward.NQEQuerySourceAtCommit, error) {
	if commitID == "" {
		head, _, err := s.Client.NQERepository.Head(ctx)
		if err != nil {
			return nil, err
		}
		commitID = string(head)
	}
	got, _, err := s.Client.NQERepository.GetQueryByID(ctx, commitID, queryID)
	if err != nil {
		if forwardNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	return got, nil
}

// OrgDirectoriesWithQueries returns every directory (with a trailing slash, root excluded) that holds at least one committed query at or below it. The
// API lists queries, not directories, so an EMPTY committed directory is not in this set: absence here means "not known to exist", not "does not exist".
func (s *Session) OrgDirectoriesWithQueries(ctx context.Context) (map[string]int, error) {
	list, _, err := s.Client.NQERepository.ListHeadQueries(ctx)
	if err != nil {
		return nil, err
	}
	dirs := map[string]int{}
	for _, q := range list {
		p := strings.TrimSpace(q.Path)
		for i := 1; i < len(p); i++ {
			if p[i] == '/' {
				dirs[p[:i+1]]++
			}
		}
	}
	return dirs, nil
}

// SaveOrgQueryInNewDirectories creates the missing enclosing directories (top-down, parents first), adds the query and commits the directories and the
// query together. On any failure the drafts are removed again (the query first, then the directories deepest first) so nothing half-finished stays in
// the workspace. dirs are written with a trailing slash; commit paths carry none.
func (s *Session) SaveOrgQueryInNewDirectories(ctx context.Context, dirs []string, path, source, title, body string) error {
	cleanup := func(cause error, added []string, queryAdded bool) error {
		var errs []error
		errs = append(errs, cause)
		if queryAdded {
			if derr := s.DiscardOrgDrafts(ctx, []string{path}); derr != nil {
				errs = append(errs, errors.New("the uncommitted query draft could not be removed: "+derr.Error()))
			}
		}
		for i := len(added) - 1; i >= 0; i-- {
			if derr := s.DiscardOrgDrafts(ctx, []string{added[i]}); derr != nil {
				errs = append(errs, errors.New("the uncommitted directory draft "+added[i]+" could not be removed: "+derr.Error()))
			}
		}
		return errors.Join(errs...)
	}
	var added []string
	for _, d := range dirs {
		if _, err := s.Client.NQERepository.AddDirectory(ctx, d); err != nil {
			return cleanup(err, added, false)
		}
		added = append(added, d)
	}
	if _, err := s.Client.NQERepository.AddQuery(ctx, path, source); err != nil {
		return cleanup(err, added, false)
	}
	// Forward refuses a commit that names a directory ("Only query edits or deletions can be included in a commit"): the directories added above are
	// part of the workspace and become committed with the query that sits in them.
	if _, err := s.Client.NQERepository.Commit(ctx, forward.NQECommitRequest{Paths: []string{path}, Message: &forward.NQECommitMessage{Title: title, Body: body}}); err != nil {
		return cleanup(err, added, true)
	}
	return nil
}

// NQEHead returns the library's head commit id.
func (s *Session) NQEHead(ctx context.Context) (string, error) {
	h, _, err := s.Client.NQERepository.Head(ctx)
	return string(h), err
}

// StageOrgQuery stages a query's new source in the caller's workspace as a draft (nothing is committed or visible to the organization until a commit). A path that is
// in the library's head is edited with Forward's editQuery; a new path is added (Forward refuses adding a path that exists, so the two are different calls).
//
// editQuery's basis is the query's id and the commit that LAST CHANGED that query (the lastCommitId of the head listing), NOT the library's head commit: Forward refuses
// a new draft whose basis commit is not the query's own last changing commit (measured on a live library whose head had moved past the module's last change).
func (s *Session) StageOrgQuery(ctx context.Context, path, source string) error {
	list, _, err := s.Client.NQERepository.ListHeadQueries(ctx)
	if err != nil {
		return err
	}
	for _, q := range list {
		if strings.TrimSpace(q.Path) != path {
			continue
		}
		qid := string(q.QueryID)
		if qid == "" || q.LastCommitID == "" {
			return errors.New("Forward's listing gave no query id or last commit for " + path + ", so an edit cannot be based on it")
		}
		_, err = s.Client.NQERepository.EditQuery(ctx, path, source, forward.NQEDraftBasis{QueryID: qid, CommitID: string(q.LastCommitID)})
		return err
	}
	_, err = s.Client.NQERepository.AddQuery(ctx, path, source)
	return err
}

// DiscardOrgDrafts drops the caller's drafts at (and under) paths. A path with no draft is not an error.
func (s *Session) DiscardOrgDrafts(ctx context.Context, paths []string) error {
	if len(paths) == 0 {
		return nil
	}
	_, err := s.Client.NQERepository.DiscardChanges(ctx, paths)
	return err
}

// DraftsAt names the paths among paths where the caller already has an uncommitted change (an add, edit or delete), so a skill can refuse to stage over, or discard, work
// that is not its own.
func (s *Session) DraftsAt(ctx context.Context, paths []string) ([]string, error) {
	drafts, _, err := s.Client.NQERepository.ListDrafts(ctx)
	if err != nil {
		return nil, err
	}
	want := map[string]bool{}
	for _, p := range paths {
		want[p] = true
	}
	var out []string
	for _, d := range drafts {
		for _, c := range []string{d.Path, d.Directory + d.Name} {
			if c != "" && want[c] {
				out = append(out, c)
			}
		}
		if d.Basis != nil && want[d.Basis.Path] {
			out = append(out, d.Basis.Path)
		}
	}
	return out, nil
}

// CommitOrgPaths commits every staged change at paths as ONE commit and returns the new head commit id (the commit call returns none, so it is read back).
func (s *Session) CommitOrgPaths(ctx context.Context, paths []string, title, body string) (string, error) {
	if _, err := s.Client.NQERepository.Commit(ctx, forward.NQECommitRequest{Paths: paths, Message: &forward.NQECommitMessage{Title: title, Body: body}}); err != nil {
		return "", err
	}
	return s.NQEHead(ctx)
}

// NQECommitDryRun types the staged changes at paths against the library (and a snapshot's data model when snapshotID is set), including the queries that
// import them. It commits nothing, but it reads what is staged.
func (s *Session) NQECommitDryRun(ctx context.Context, paths []string, snapshotID string) (*forward.NQECommitDryRun, error) {
	d, _, err := s.Client.NQERepository.CommitDryRun(ctx, paths, snapshotID)
	return d, err
}

// OrgModuleSource reads a library module's source by path at a commit ("" is the head). A missing module is (_, false, nil).
func (s *Session) OrgModuleSource(ctx context.Context, commitID, path string) (string, bool, error) {
	if commitID == "" {
		h, err := s.NQEHead(ctx)
		if err != nil {
			return "", false, err
		}
		commitID = h
	}
	q, _, err := s.Client.NQERepository.GetQuery(ctx, commitID, path)
	if err != nil {
		if forwardNotFound(err) {
			return "", false, nil
		}
		return "", false, err
	}
	return q.SourceCode, true, nil
}

// OrgQuerySourceByID reads a saved query's source by its id at a commit ("" is the head).
func (s *Session) OrgQuerySourceByID(ctx context.Context, commitID, queryID string) (string, bool, error) {
	q, err := s.OrgQueryByIDAt(ctx, queryID, commitID)
	if err != nil || q == nil {
		return "", false, err
	}
	return q.SourceCode, true, nil
}

// OrgCommitInfo says what a library commit last changed and who made it. Forward has no org-wide commit list: this is read from the head listing (each query's
// last changing commit) and the history of one of those queries.
type OrgCommitInfo struct {
	CommitID    string
	Paths       []string // queries whose last changing commit is this one (a later commit may have changed others)
	Author      string
	AuthorEmail string
	CommittedAt string
	Title       string
	Body        string
	HistoryRead bool // false when no history entry for the commit could be read
}

// OrgCommitChanges reads what a library commit last changed. commitID "" or "head" is the head commit.
func (s *Session) OrgCommitChanges(ctx context.Context, commitID string) (*OrgCommitInfo, error) {
	if commitID == "" || commitID == "head" {
		h, err := s.NQEHead(ctx)
		if err != nil {
			return nil, err
		}
		commitID = h
	}
	list, _, err := s.Client.NQERepository.ListHeadQueries(ctx)
	if err != nil {
		return nil, err
	}
	info := &OrgCommitInfo{CommitID: commitID}
	var ids []string
	for _, q := range list {
		if string(q.LastCommitID) == commitID {
			info.Paths = append(info.Paths, strings.TrimSpace(q.Path))
			ids = append(ids, string(q.QueryID))
		}
	}
	sort.Strings(info.Paths)
	for i, id := range ids {
		if i == 3 || id == "" {
			break
		}
		hist, _, herr := s.Client.NQERepository.History(ctx, id)
		if herr != nil {
			continue
		}
		for _, c := range hist {
			if string(c.ID) == commitID {
				info.Author, info.AuthorEmail, info.CommittedAt, info.Title, info.Body, info.HistoryRead = c.Author, c.AuthorEmail, c.CommittedAt, c.Title, c.Body, true
				return info, nil
			}
		}
	}
	return info, nil
}
