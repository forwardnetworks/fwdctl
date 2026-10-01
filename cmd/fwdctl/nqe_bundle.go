package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/forwardnetworks/fwdctl/fwd"
	"github.com/forwardnetworks/fwdctl/nqelint"
)

type pathFiles map[string]string

func (p *pathFiles) String() string { return fmt.Sprint(map[string]string(*p)) }
func (p *pathFiles) Set(v string) error {
	k, f, ok := strings.Cut(v, "=")
	if !ok || strings.TrimSpace(k) == "" || strings.TrimSpace(f) == "" {
		return fmt.Errorf("%q must be LIBRARY_PATH=FILE", v)
	}
	if *p == nil {
		*p = pathFiles{}
	}
	(*p)[strings.TrimSpace(k)] = strings.TrimSpace(f)
	return nil
}

// librarySource resolves a module for a bundle: a local override first, then a module that exists only locally, then the library at the commit.
type librarySource struct {
	ctx       context.Context
	sess      *fwd.Session
	commit    string
	overrides map[string]string
	added     map[string]string
}

// libPath is the library path with its leading slash: an import writes "Team/Mod", the library API and --override write "/Team/Mod".
func libPath(p string) string {
	p = strings.TrimSpace(p)
	if !strings.HasPrefix(p, "/") {
		return "/" + p
	}
	return p
}

func (l librarySource) Source(path string) (string, error) {
	path = libPath(path)
	for _, m := range []map[string]string{l.overrides, l.added} {
		for k, f := range m {
			if libPath(k) != path {
				continue
			}
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

// nqeBundleCmd prints ONE self-contained query: the entry query and every library module it imports at a commit, with local files substituted. Inline text always
// imports against the library head, and a commit id cannot be combined with inline text, so testing an older or an uncommitted version of a module needs this.
//
//	fwdctl nqe bundle (--query-id Q_... | --path /Lib/Entry) [--commit-id C] [--override /Lib/Mod=file.nqe]... [--add-module /Lib/New=file.nqe]... [--out FILE]
func nqeBundleCmd(args []string, stdout, stderr io.Writer, session func() (*fwd.Session, error)) int {
	fs := flag.NewFlagSet("nqe bundle", flag.ContinueOnError)
	fs.SetOutput(stderr)
	queryID := fs.String("query-id", "", "the entry query, by id")
	path := fs.String("path", "", "the entry query, by library path")
	commit := fs.String("commit-id", "", "the library commit to read modules at (default: the head)")
	out := fs.String("out", "", "write the bundle to this file (default: stdout)")
	var overrides, added pathFiles
	fs.Var(&overrides, "override", "LIBRARY_PATH=FILE: use this local file instead of the module (or the entry) at that path; repeatable")
	fs.Var(&added, "add-module", "LIBRARY_PATH=FILE: a module that exists only locally, importable by the entry or an override; repeatable")
	if err := fs.Parse(args); err != nil {
		return usage
	}
	if (*queryID == "") == (*path == "") {
		fmt.Fprintln(stderr, "error: usage: fwdctl nqe bundle (--query-id Q_... | --path /Lib/Entry) [--commit-id C] [--override PATH=FILE]... [--add-module PATH=FILE]... [--out FILE]")
		return usage
	}
	sess, err := session()
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return usage
	}
	ctx := context.Background()
	src := librarySource{ctx: ctx, sess: sess, commit: *commit, overrides: overrides, added: added}
	var entry string
	switch {
	case *path != "":
		entry, err = src.Source(*path)
	default:
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
