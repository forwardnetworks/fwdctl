package skills

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/forwardnetworks/fwdctl/fwd"
	"github.com/forwardnetworks/fwdctl/result"
)

const findNQEName = "find-nqe-query"

func init() { Register(findNQEName, findNQE) }

type findNQEInput struct {
	NetworkID string `json:"network_id"`
	Question  string `json:"question"`
	QueryID   string `json:"query_id"`
	// CommitID reads query_id or path at a library commit instead of the head; the result names the commit.
	CommitID  string `json:"commit_id"`
	Path      string `json:"path"`
	Directory string `json:"directory"`
	// List shows the library as a tree instead of searching it: the directories directly under Directory (default /) with how many queries are below each, and the
	// queries directly in it.
	List  bool `json:"list"`
	Limit int  `json:"limit"`
}

const (
	defaultFindLimit = 8
	maxFindLimit     = 25
)

var wordRe = regexp.MustCompile(`[a-z0-9]+`)

// stop words carry no signal in a query's path or intent.
var stop = map[string]bool{"the": true, "a": true, "an": true, "of": true, "to": true, "in": true, "on": true, "for": true, "and": true, "or": true,
	"is": true, "are": true, "all": true, "any": true, "show": true, "me": true, "my": true, "our": true, "with": true, "that": true, "which": true, "what": true, "do": true, "does": true}

func words(s string) map[string]bool {
	out := map[string]bool{}
	for _, w := range wordRe.FindAllString(strings.ToLower(s), -1) {
		if len(w) > 1 && !stop[w] {
			out[w] = true
		}
	}
	return out
}

func findNQE(ctx context.Context, s *fwd.Session, raw json.RawMessage) (result.Result, error) {
	var in findNQEInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return result.Result{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	if in.Limit <= 0 {
		in.Limit = defaultFindLimit
	}
	in.Limit = min(in.Limit, maxFindLimit)
	cx := result.Context{NetworkID: in.NetworkID, State: "current"}
	if in.QueryID != "" || in.Path != "" {
		if in.Question != "" || (in.QueryID != "" && in.Path != "") {
			return result.Result{}, fmt.Errorf("%w: give one of question, query_id or path", ErrInvalidInput)
		}
		return readNQEQuery(ctx, s, in, cx)
	}
	if in.List {
		return listNQEDirectory(ctx, s, in, cx)
	}
	if in.CommitID != "" && in.Question == "" {
		return describeNQECommit(ctx, s, in, cx)
	}
	want := words(in.Question)
	if len(want) == 0 {
		return result.NewError(findNQEName, "question has no searchable words", cx), nil
	}
	lib, err := s.NQELibrary(ctx, in.Directory)
	if err != nil {
		return result.Result{}, err
	}
	type hit struct {
		score int
		row   map[string]any
	}
	var hits []hit
	for _, q := range lib {
		text := words(q.Path + " " + q.Intent)
		score := 0
		for w := range want {
			if text[w] {
				score++
			}
		}
		if score == 0 {
			continue
		}
		hits = append(hits, hit{score, map[string]any{"query_id": q.QueryID, "path": q.Path, "intent": q.Intent, "repository": q.Repository, "matched_words": score}})
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].score > hits[j].score })
	limits := []string{fmt.Sprintf("matched by words in each query's path and stated intent (%d queries searched), not by reading its source", len(lib))}
	if len(hits) == 0 {
		return result.NewUnknown(findNQEName, "No saved query matches the question", cx,
			append(limits, "no match does not mean no saved query exists: the wording may differ"), result.Options{NextActions: []string{"author-nqe-query"}})
	}
	if len(hits) > in.Limit {
		limits = append(limits, fmt.Sprintf("%d queries matched; the best %d are shown", len(hits), in.Limit))
		hits = hits[:in.Limit]
	}
	rows := make([]map[string]any, len(hits))
	for i, h := range hits {
		rows[i] = h.row
	}
	return result.Build(findNQEName, result.OK, fmt.Sprintf("%d saved query(ies) look relevant; best: %v", len(rows), rows[0]["path"]), result.Inferred, cx,
		result.Options{Limits: limits, NextActions: []string{"validate-nqe-query", "check-network-compliance", "compare-nqe-results"},
			Evidence: []result.Evidence{result.NewEvidence(result.EvNQE, "listNqeQueries", nil, map[string]any{"question": in.Question, "queries": rows}, fmt.Sprintf("%d matches", len(rows)))}})
}

// readNQEQuery returns the source of one saved query of the organization's library, by its id (Q_...) or its path. The built-in Forward
// library (FQ_ ids) is searched by find-nqe-query but its source is not read here.
func readNQEQuery(ctx context.Context, s *fwd.Session, in findNQEInput, cx result.Context) (result.Result, error) {
	var src, intent, id, path, commit string
	found := false
	if in.QueryID != "" {
		if !strings.HasPrefix(in.QueryID, "Q_") {
			return result.NewUnknown(findNQEName, fmt.Sprintf("%s is not an organization query id; only Q_ ids can be read", in.QueryID), cx,
				[]string{"the built-in library (FQ_ ids) is listed by a question but its source is not read by this skill"}, result.Options{})
		}
		q, err := s.OrgQueryByIDAt(ctx, in.QueryID, in.CommitID)
		if err != nil {
			return result.Result{}, err
		}
		if q != nil {
			src, intent, id, found = q.SourceCode, q.Intent, in.QueryID, true
			commit = firstNonEmpty(in.CommitID, "head")
		}
	} else {
		q, err := s.OrgQueryAt(ctx, strings.TrimSpace(in.Path), in.CommitID)
		if err != nil {
			return result.Result{}, err
		}
		if q != nil {
			src, id, path, found = q.Source, q.QueryID, q.Path, true
			commit = q.CommitID
		}
	}
	if !found {
		return result.NewUnknown(findNQEName, "The organization's library holds no such query", cx,
			[]string{"matched exactly by id or path against the organization's library at the head or at the commit_id given (a path is looked up among the head's paths, so a query deleted since is found by its id, not its path)"}, result.Options{NextActions: []string{"author-nqe-query"}})
	}
	return result.Build(findNQEName, result.OK, fmt.Sprintf("Read the saved query %s (%d bytes)", map[bool]string{true: id, false: path}[in.QueryID != ""], len(src)), result.Deterministic, cx,
		result.Options{NextActions: []string{"validate-nqe-query", "edit-nqe-query"},
			Evidence: []result.Evidence{result.NewEvidence(result.EvNQE, "getQuery", nil, map[string]any{"query_id": id, "path": path, "intent": intent, "commit_id": nilIfEmpty(commit), "source": src}, "")}})
}

// listNQEDirectory shows one level of the query library: the directories under in.Directory (default /) with the number of queries below each, and the queries directly in it.
// The library is the organization's committed queries (Q_ ids) and Forward's built-in ones (FQ_ ids), told apart by their repository.
func listNQEDirectory(ctx context.Context, s *fwd.Session, in findNQEInput, cx result.Context) (result.Result, error) {
	dir := strings.TrimSpace(in.Directory)
	if dir == "" {
		dir = "/"
	}
	if !strings.HasPrefix(dir, "/") || strings.Contains(dir, "..") {
		return result.Result{}, fmt.Errorf("%w: directory is a library directory starting with / (for example /L3/), or / for the top", ErrInvalidInput)
	}
	if !strings.HasSuffix(dir, "/") {
		dir += "/"
	}
	lib, err := s.NQELibrary(ctx, "")
	if err != nil {
		return result.Result{}, err
	}
	type dirInfo struct {
		Path    string `json:"path"`
		Queries int    `json:"queries_below"`
		Org     int    `json:"organization"`
		Forward int    `json:"forward_builtin"`
	}
	dirs := map[string]*dirInfo{}
	var direct []map[string]any
	for _, q := range lib {
		p := strings.TrimSpace(q.Path)
		if !strings.HasPrefix(p, dir) {
			continue
		}
		rest := p[len(dir):]
		if i := strings.Index(rest, "/"); i >= 0 {
			name := dir + rest[:i+1]
			d := dirs[name]
			if d == nil {
				d = &dirInfo{Path: name}
				dirs[name] = d
			}
			d.Queries++
			if strings.HasPrefix(q.QueryID, "Q_") {
				d.Org++
			} else {
				d.Forward++
			}
			continue
		}
		direct = append(direct, map[string]any{"path": p, "query_id": q.QueryID, "intent": q.Intent})
	}
	list := make([]dirInfo, 0, len(dirs))
	for _, d := range dirs {
		list = append(list, *d)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Path < list[j].Path })
	sort.Slice(direct, func(i, j int) bool { return fmt.Sprint(direct[i]["path"]) < fmt.Sprint(direct[j]["path"]) })
	limits := []string{fmt.Sprintf("the library holds %d queries; only one level of %s is shown", len(lib), dir),
		"a directory exists only while a query is under it, so this list is the set of directories a new query can be saved into (a new directory needs create_directory in edit-nqe-query)"}
	if len(direct) > maxFindLimit*4 {
		limits = append(limits, fmt.Sprintf("%d queries are directly in %s; the first %d are shown", len(direct), dir, maxFindLimit*4))
		direct = direct[:maxFindLimit*4]
	}
	if len(list) == 0 && len(direct) == 0 {
		return result.NewUnknown(findNQEName, fmt.Sprintf("The library has no directory or query under %s", dir), cx, append(limits, "no such directory (a directory exists only while a query is in it)"),
			result.Options{NextActions: []string{"edit-nqe-query"}})
	}
	return result.Build(findNQEName, result.OK, fmt.Sprintf("%s holds %d director%s and %d quer%s directly", dir, len(list), map[bool]string{true: "y", false: "ies"}[len(list) == 1], len(direct), map[bool]string{true: "y", false: "ies"}[len(direct) == 1]),
		result.Deterministic, cx, result.Options{Limits: limits, NextActions: []string{"validate-nqe-query", "edit-nqe-query"},
			Evidence: []result.Evidence{result.NewEvidence(result.EvNQE, "listNqeQueries", nil, map[string]any{"directory": dir, "directories": list, "queries": direct}, fmt.Sprintf("%d directories", len(list)))}})
}

// describeNQECommit answers "what is this library commit and who made it": the paths whose last change it was, and the commit's author, time and title. commit_id "head" is the head.
func describeNQECommit(ctx context.Context, s *fwd.Session, in findNQEInput, cx result.Context) (result.Result, error) {
	c, err := s.OrgCommitChanges(ctx, in.CommitID)
	if err != nil {
		return result.Result{}, err
	}
	limits := []string{"Forward has no list of library commits: this reads the queries whose LAST change was this commit (a later commit may have changed others since) and the history of one of them for the author, time and title"}
	if len(c.Paths) == 0 {
		return result.NewUnknown(findNQEName, fmt.Sprintf("No query's last change is commit %s", c.CommitID), cx,
			append(limits, "the commit may be older than the latest change of every query it touched, may have deleted queries, or may not exist: that is not proof it changed nothing"), result.Options{})
	}
	paths := c.Paths
	if len(paths) > in.Limit {
		limits = append(limits, fmt.Sprintf("%d queries; the first %d are shown", len(paths), in.Limit))
		paths = paths[:in.Limit]
	}
	d := map[string]any{"commit_id": c.CommitID, "queries_last_changed_here": len(c.Paths), "paths": paths}
	finding := fmt.Sprintf("Commit %s is the last change of %d query(ies)", c.CommitID, len(c.Paths))
	if c.HistoryRead {
		d["author"], d["author_email"], d["committed_at"], d["title"] = c.Author, c.AuthorEmail, c.CommittedAt, c.Title
		finding += fmt.Sprintf(", made by %s at %s: %q", firstNonEmpty(c.Author, c.AuthorEmail), c.CommittedAt, c.Title)
	} else {
		limits = append(limits, "the history of the queries did not list this commit, so its author, time and title are not known")
	}
	limits = append(limits, "an author and a title are personal and internal: keep them out of issues and public places")
	return result.Build(findNQEName, result.OK, finding, result.Deterministic, cx, result.Options{Limits: limits,
		Evidence: []result.Evidence{result.NewEvidence(result.EvNQE, "orgCommit", nil, d, finding)}})
}
