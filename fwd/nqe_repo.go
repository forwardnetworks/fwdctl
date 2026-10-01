package fwd

import (
	"context"
	"errors"
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

// SaveOrgQuery adds or replaces a query and commits it to the organization's library. When the commit fails the draft is removed again so
// nothing half-finished stays in the workspace.
func (s *Session) SaveOrgQuery(ctx context.Context, path, source, title, body string) error {
	if _, err := s.Client.NQERepository.AddQuery(ctx, path, source); err != nil {
		return err
	}
	_, err := s.Client.NQERepository.Commit(ctx, forward.NQECommitRequest{Paths: []string{path}, Message: &forward.NQECommitMessage{Title: title, Body: body}})
	if err != nil {
		if _, derr := s.Client.NQERepository.DeleteQuery(ctx, path); derr != nil {
			return errors.Join(err, errors.New("the uncommitted draft could not be removed either: "+derr.Error()))
		}
		return err
	}
	return nil
}

// DeleteOrgQuery removes a query from the organization's library and commits the removal.
func (s *Session) DeleteOrgQuery(ctx context.Context, path, title, body string) error {
	if _, err := s.Client.NQERepository.DeleteQuery(ctx, path); err != nil {
		return err
	}
	_, err := s.Client.NQERepository.Commit(ctx, forward.NQECommitRequest{Paths: []string{path}, Message: &forward.NQECommitMessage{Title: title, Body: body}})
	return err
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
			if _, derr := s.Client.NQERepository.DeleteQuery(ctx, path); derr != nil {
				errs = append(errs, errors.New("the uncommitted query draft could not be removed: "+derr.Error()))
			}
		}
		for i := len(added) - 1; i >= 0; i-- {
			if _, derr := s.Client.NQERepository.DeleteDirectory(ctx, added[i]); derr != nil {
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

// StageOrgQuery stages a query's new source in the caller's workspace (a draft: nothing is committed or visible to the organization until a commit).
// Forward has no discard, so a staged draft is undone by staging the committed source again.
func (s *Session) StageOrgQuery(ctx context.Context, path, source string) error {
	_, err := s.Client.NQERepository.AddQuery(ctx, path, source)
	return err
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
