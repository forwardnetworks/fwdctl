package main

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/forwardnetworks/fwdctl/nqelint"
)

// nqeExportOpts are the flags of `fwdctl nqe export`.
type nqeExportOpts struct {
	queryID, path, commit, out, zipFile, uiZip string
	overrides, added                           []string
	force                                      bool
}

type exportFile struct {
	LibraryPath    string   `json:"library_path"`
	File           string   `json:"file"`
	QueryID        string   `json:"query_id,omitempty"`
	CommitID       string   `json:"commit_id,omitempty"`
	SHA256         string   `json:"sha256"`
	Imports        []string `json:"imports"`
	BuiltinImports []string `json:"builtin_imports,omitempty"`
	LocalFile      string   `json:"local_file,omitempty"` // set when an --override or --add-module supplied the text instead of the library
	Entry          bool     `json:"entry,omitempty"`
	src            string
}

type exportData struct {
	NQEName  string   `json:"nqe_name"`
	DataFile string   `json:"data_file,omitempty"`
	Type     string   `json:"type,omitempty"`
	Status   string   `json:"status"` // found | not_found | unchecked
	UsedBy   []string `json:"used_by"`
}

type exportManifest struct {
	Format    string       `json:"format"`
	Fwdctl    string       `json:"fwdctl"`
	CommitID  string       `json:"commit_id"`
	Entry     string       `json:"entry"`
	Files     []exportFile `json:"files"`
	DataFiles []exportData `json:"data_files"`
	Notes     []string     `json:"notes"`
}

// nqeExportCmd writes the entry query and every library module it imports (transitively) to a folder tree that mirrors the library: one <name>.nqe per query, import
// statements untouched (an import already names the library path, so the tree re-imports cleanly), plus manifest.json. Unlike `nqe bundle` it does not inline anything.
func nqeExportCmd(a *app, o nqeExportOpts) int {
	stderr := a.err
	fail := func(code int, f string, v ...any) int { fmt.Fprintf(stderr, "error: "+f+"\n", v...); return code }
	overrides, perr := parsePathFiles(o.overrides)
	if perr != nil {
		return fail(usage, "--override %v", perr)
	}
	added, perr := parsePathFiles(o.added)
	if perr != nil {
		return fail(usage, "--add-module %v", perr)
	}
	if (o.queryID == "") == (o.path == "") {
		return fail(usage, "give exactly one of --query-id and --path")
	}
	if o.out == "" {
		return fail(usage, "--out DIR is required")
	}
	if entries, err := os.ReadDir(o.out); err == nil && len(entries) > 0 && !o.force {
		return fail(usage, "%s is not empty; export into an empty directory, or pass --force to write into it", o.out)
	}
	sess, err := a.session()
	if err != nil {
		return fail(usage, "%v", err)
	}
	ctx := context.Background()
	commit := o.commit
	if commit == "" {
		if commit, err = sess.NQEHead(ctx); err != nil {
			return fail(3, "reading the library head: %v", err)
		}
	}
	src := librarySource{ctx: ctx, sess: sess, commit: commit, overrides: overrides, added: added, used: map[string]bool{}}

	// the entry: by path, or by id (whose library path comes from the head listing)
	entryPath := ""
	var entryID string
	if o.path != "" {
		entryPath = libPath(o.path)
	} else {
		p, err := sess.OrgQueryPathByID(ctx, o.queryID)
		if err != nil {
			return fail(3, "looking up the entry's path: %v", err)
		}
		if p == "" {
			return fail(1, "query %s is not in the library head, so its library path is unknown; give --path", o.queryID)
		}
		entryPath, entryID = p, o.queryID
	}

	files := map[string]*exportFile{}
	var order []string
	var load func(path string, isEntry bool) error
	load = func(path string, isEntry bool) error {
		if files[path] != nil {
			return nil
		}
		f := &exportFile{LibraryPath: path, CommitID: commit, Entry: isEntry}
		if lf, ok := src.override(path); ok {
			b, err := os.ReadFile(lf)
			if err != nil {
				return err
			}
			src.used[path] = true
			f.src, f.LocalFile, f.CommitID = string(b), lf, ""
		} else if isEntry && entryID != "" {
			text, ok, err := sess.OrgQuerySourceByID(ctx, commit, entryID)
			if err != nil {
				return fmt.Errorf("reading the entry %s: %w", path, err)
			}
			if !ok {
				return fmt.Errorf("the library has no query %s at commit %s", entryID, commit)
			}
			f.src, f.QueryID = text, entryID
		} else {
			text, id, ok, err := sess.OrgModuleAt(ctx, commit, path)
			if err != nil {
				return fmt.Errorf("reading %s: %w", path, err)
			}
			if !ok {
				return fmt.Errorf("the library holds no query %s at commit %s", path, commit)
			}
			f.src, f.QueryID = text, id
		}
		prog, diags := nqelint.Parse(f.src)
		if prog == nil || nqelint.HasErrors(diags) {
			msg := "does not parse"
			for _, d := range diags {
				if d.Severity == "error" {
					msg = fmt.Sprintf("line %d: %s", d.Line, d.Message)
					break
				}
			}
			return fmt.Errorf("%s %s", path, msg)
		}
		sum := sha256.Sum256([]byte(f.src))
		f.SHA256 = hex.EncodeToString(sum[:])
		f.File = exportFileName(path)
		files[path] = f
		order = append(order, path)
		for _, ip := range prog.Imports {
			if strings.HasPrefix(ip, "@fwd/") {
				f.BuiltinImports = append(f.BuiltinImports, ip)
				continue
			}
			lp := libPath(ip)
			f.Imports = append(f.Imports, lp)
			if err := load(lp, false); err != nil {
				return fmt.Errorf("%s imports %q: %w", path, ip, err)
			}
		}
		sort.Strings(f.Imports)
		return nil
	}
	if err := load(entryPath, true); err != nil {
		return fail(1, "%v", err)
	}
	for _, m := range []map[string]string{overrides, added} {
		for k := range m {
			if !src.used[libPath(k)] {
				return fail(1, "--override/--add-module path %s matched neither the entry nor any module it imports; nothing was written", k)
			}
		}
	}
	// two library paths that would land on one file name (a path segment of ".." or an empty one is refused in exportFileName)
	seen := map[string]string{}
	for _, p := range order {
		if f := files[p]; f.File == "" {
			return fail(1, "library path %q cannot be written as a file (empty or ..-containing segment); nothing was written", p)
		} else if prev, dup := seen[strings.ToLower(f.File)]; dup {
			return fail(1, "%q and %q would write the same file name (case-insensitively); nothing was written", prev, p)
		}
		seen[strings.ToLower(files[p].File)] = p
	}
	sort.Slice(order, func(i, j int) bool {
		if files[order[i]].Entry != files[order[j]].Entry {
			return files[order[i]].Entry
		}
		return order[i] < order[j]
	})

	// the data files the queries read: an export that needs one is not self-contained
	usedBy := map[string][]string{}
	for _, p := range order {
		for _, n := range nqelint.ExtensionRefs(files[p].src) {
			usedBy[n] = append(usedBy[n], p)
		}
	}
	var data []exportData
	known, lerr := sess.DataFiles(ctx)
	for n, by := range usedBy {
		d := exportData{NQEName: n, Status: "unchecked", UsedBy: by}
		if lerr == nil {
			d.Status = "not_found"
			for _, df := range known {
				if df.NQEName == n {
					d.DataFile, d.Type, d.Status = df.Name, string(df.Type), "found"
				}
			}
		}
		data = append(data, d)
	}
	sort.Slice(data, func(i, j int) bool { return data[i].NQEName < data[j].NQEName })

	m := exportManifest{Format: "fwdctl-nqe-export/1", Fwdctl: version, CommitID: commit, Entry: entryPath, DataFiles: data, Notes: []string{
		"one <query name>.nqe per library query, under the library path; import statements are unchanged and already name library paths, so this tree re-imports as it is",
		"imports of @fwd/... stay as written: they resolve against Forward's built-in library, which a commit does not version",
		"this tree, its manifest and any --zip of it are fwdctl's own format and are NOT what the Forward UI imports (the UI reads a zip holding queries-export.proto: --ui-zip writes that); `fwdctl nqe pack` reads this tree back",
		"data_files lists the organization data files the queries read as network.extensions.<nqe_name>; they are not part of this export and must be attached to the network the query runs on",
	}}
	for _, p := range order {
		m.Files = append(m.Files, *files[p])
	}
	mb, _ := json.MarshalIndent(m, "", "  ")
	mb = append(mb, '\n')

	if err := os.MkdirAll(o.out, 0o755); err != nil {
		return fail(3, "%v", err)
	}
	for _, p := range order {
		dst := filepath.Join(o.out, filepath.FromSlash(files[p].File))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return fail(3, "%v", err)
		}
		if err := os.WriteFile(dst, []byte(files[p].src), 0o644); err != nil {
			return fail(3, "%v", err)
		}
	}
	if err := os.WriteFile(filepath.Join(o.out, "manifest.json"), mb, 0o644); err != nil {
		return fail(3, "%v", err)
	}
	if o.zipFile != "" {
		var buf bytes.Buffer
		zw := zip.NewWriter(&buf)
		add := func(name string, b []byte) error {
			w, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Deflate})
			if err != nil {
				return err
			}
			_, err = w.Write(b)
			return err
		}
		for _, p := range order {
			if err := add(files[p].File, []byte(files[p].src)); err != nil {
				return fail(3, "%v", err)
			}
		}
		if err := add("manifest.json", mb); err != nil {
			return fail(3, "%v", err)
		}
		if err := zw.Close(); err != nil {
			return fail(3, "%v", err)
		}
		if err := os.WriteFile(o.zipFile, buf.Bytes(), 0o644); err != nil {
			return fail(3, "%v", err)
		}
	}
	if o.uiZip != "" {
		qs := make([]uiQuery, 0, len(order))
		for _, p := range order {
			qs = append(qs, uiQuery{Path: p, Source: files[p].src})
		}
		zb, err := zipUIPackage(qs)
		if err != nil {
			return fail(3, "%v", err)
		}
		if err := os.WriteFile(o.uiZip, zb, 0o644); err != nil {
			return fail(3, "%v", err)
		}
		fmt.Fprintf(stderr, "wrote %s in the Forward UI's library import format (queries-export.proto); it holds the queries only, not the data files they read\n", o.uiZip)
	}
	fmt.Fprintf(stderr, "exported %d quer%s to %s at commit %s (manifest.json lists each file's query id, commit, sha256 and imports)\n", len(order), map[bool]string{true: "y", false: "ies"}[len(order) == 1], o.out, commit)
	for _, d := range data {
		switch d.Status {
		case "found":
			fmt.Fprintf(stderr, "note: reads organization data file %s (network.extensions.%s), which is not part of this export\n", d.DataFile, d.NQEName)
		case "not_found":
			fmt.Fprintf(stderr, "warning: reads network.extensions.%s but no organization data file has that nqe_name\n", d.NQEName)
		default:
			fmt.Fprintf(stderr, "note: reads network.extensions.%s; the data files could not be listed, so whether it exists is unchecked\n", d.NQEName)
		}
	}
	return 0
}

// exportFileName is the relative file for a library path: "/A/B/Name" is "A/B/Name.nqe". Empty when a segment is empty or "..".
func exportFileName(libraryPath string) string {
	parts := strings.Split(strings.TrimPrefix(libraryPath, "/"), "/")
	for _, s := range parts {
		if s == "" || s == "." || s == ".." || strings.ContainsRune(s, 0) {
			return ""
		}
	}
	return strings.Join(parts, "/") + ".nqe"
}
