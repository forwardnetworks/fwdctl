package main

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/forwardnetworks/fwdctl/fwd"
	"github.com/forwardnetworks/fwdctl/nqelint"
)

type pathFiles map[string]string

// parsePathFiles reads repeated LIBRARY_PATH=FILE flag values.
func parsePathFiles(vals []string) (pathFiles, error) {
	out := pathFiles{}
	for _, v := range vals {
		k, f, ok := strings.Cut(v, "=")
		if !ok || strings.TrimSpace(k) == "" || strings.TrimSpace(f) == "" {
			return nil, fmt.Errorf("%q must be LIBRARY_PATH=FILE", v)
		}
		out[strings.TrimSpace(k)] = strings.TrimSpace(f)
	}
	return out, nil
}

// librarySource resolves a module for a bundle: a local override first, then a module that exists only locally, then the library at the commit.
type librarySource struct {
	ctx       context.Context
	sess      *fwd.Session
	commit    string
	overrides map[string]string
	added     map[string]string
	used      map[string]bool // the override and added paths the bundle actually read, so one that matched nothing is an error
}

// libPath is the library path with its leading slash: an import writes "Team/Mod", the library API and --override write "/Team/Mod".
func libPath(p string) string {
	p = strings.TrimSpace(p)
	if !strings.HasPrefix(p, "/") {
		return "/" + p
	}
	return p
}

// override reports a local file given for path (an --override or an --add-module), without reading it.
func (l librarySource) override(path string) (string, bool) {
	path = libPath(path)
	for _, m := range []map[string]string{l.overrides, l.added} {
		for k, f := range m {
			if libPath(k) == path {
				return f, true
			}
		}
	}
	return "", false
}

func (l librarySource) Source(path string) (string, error) {
	path = libPath(path)
	for _, m := range []map[string]string{l.overrides, l.added} {
		for k, f := range m {
			if libPath(k) != path {
				continue
			}
			l.used[libPath(k)] = true
			b, err := os.ReadFile(f)
			return string(b), err
		}
	}
	src, ok, err := l.sess.OrgModuleSource(l.ctx, l.commit, path)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", nqelint.ErrNoSuchModule
	}
	return src, nil
}

// nqeBundleOpts are the flags of `fwdctl nqe bundle`.
type nqeBundleOpts struct {
	queryID, path, commit, out string
	overrides, added           []string
}

// nqeBundleCmd prints ONE self-contained query: the entry query and every library module it imports at a commit, with local files substituted. Inline text always
// imports against the library head, and a commit id cannot be combined with inline text, so testing an older or an uncommitted version of a module needs this.
func nqeBundleCmd(a *app, o nqeBundleOpts) int {
	stdout, stderr, session := a.out, a.err, a.session
	queryID, path, commit, out := &o.queryID, &o.path, &o.commit, &o.out
	overrides, perr := parsePathFiles(o.overrides)
	if perr != nil {
		fmt.Fprintf(stderr, "error: --override %v\n", perr)
		return usage
	}
	added, perr := parsePathFiles(o.added)
	if perr != nil {
		fmt.Fprintf(stderr, "error: --add-module %v\n", perr)
		return usage
	}
	if (*queryID == "") == (*path == "") {
		fmt.Fprintln(stderr, "error: give exactly one of --query-id and --path")
		return usage
	}
	sess, err := session()
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return usage
	}
	ctx := context.Background()
	src := librarySource{ctx: ctx, sess: sess, commit: *commit, overrides: overrides, added: added, used: map[string]bool{}}
	var entry string
	switch {
	case *path != "":
		entry, err = src.Source(*path)
	default:
		// an override of the entry's own path replaces the entry body: find the path of the query id (at the head; a query renamed since is not found, and an override that
		// then matches nothing is reported below)
		if len(overrides) > 0 {
			if p, perr := sess.OrgQueryPathByID(ctx, *queryID); perr != nil {
				fmt.Fprintf(stderr, "error: looking up the entry's path: %v\n", perr)
				return 3
			} else if p != "" {
				if _, ok := src.override(p); ok {
					entry, err = src.Source(p)
					break
				}
			}
		}
		var ok bool
		entry, ok, err = sess.OrgQuerySourceByID(ctx, *commit, *queryID)
		if err == nil && !ok {
			err = fmt.Errorf("the library has no query %s at that commit", *queryID)
		}
	}
	if err != nil {
		fmt.Fprintf(stderr, "error: reading the entry query: %v\n", err)
		return 3
	}
	text, inlined, err := nqelint.Bundle(entry, src)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	// an override or an added module the bundle never read matched nothing: refuse, or a before/after comparison would silently compare a program with itself
	var unused []string
	for _, m := range []map[string]string{overrides, added} {
		for k := range m {
			if !src.used[libPath(k)] {
				unused = append(unused, k)
			}
		}
	}
	if len(unused) > 0 {
		sort.Strings(unused)
		fmt.Fprintf(stderr, "error: --override/--add-module path(s) not used by the bundle (they matched neither the entry nor any module it imports): %s; nothing was written\n", strings.Join(unused, ", "))
		return 1
	}
	if *out != "" {
		if err := os.WriteFile(*out, []byte(text), 0o644); err != nil {
			fmt.Fprintf(stderr, "error: %v\n", err)
			return 3
		}
	} else {
		fmt.Fprint(stdout, text)
	}
	c := *commit
	if c == "" {
		c = "head"
	}
	fmt.Fprintf(stderr, "bundled the entry and %d module(s) at commit %s (%d override(s), %d added); run it with: fwdctl nqe run --network ID --file FILE\n", len(inlined), c, len(overrides), len(added))
	return 0
}
