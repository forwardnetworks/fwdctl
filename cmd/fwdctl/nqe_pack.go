package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/forwardnetworks/fwdctl/skills"
)

// nqePackOpts are the flags of `fwdctl nqe pack`.
type nqePackOpts struct {
	message, basis, out                     string
	createDirectory, typecheck, changedOnly bool
}

// nqePackCmd turns a folder tree (as `nqe export` writes it, or any tree of <name>.nqe files) into the input of `edit-nqe-query` with changes, so the tree goes back to a library as
// ONE commit. It only reads the directory and prints JSON: the skill stays free of filesystem reads, and the write itself is the skill's own dry run, approval and apply.
func nqePackCmd(a *app, dir string, o nqePackOpts) int {
	fail := func(code int, f string, v ...any) int { fmt.Fprintf(a.err, "error: "+f+"\n", v...); return code }
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return fail(usage, "%s is not a directory", dir)
	}
	// the manifest, when there is one, says what each file was at export time
	var m exportManifest
	haveManifest := false
	if b, err := os.ReadFile(filepath.Join(dir, "manifest.json")); err == nil {
		if json.Unmarshal(b, &m) != nil || m.Format != "fwdctl-nqe-export/1" {
			return fail(1, "%s/manifest.json is not a fwdctl nqe export manifest", dir)
		}
		haveManifest = true
	}
	if o.changedOnly && !haveManifest {
		return fail(usage, "--changed-only compares with manifest.json, and %s has none", dir)
	}
	exported := map[string]exportFile{} // library path -> manifest entry
	for _, f := range m.Files {
		exported[f.LibraryPath] = f
	}
	basis := o.basis
	if basis == "manifest" {
		if !haveManifest {
			return fail(usage, "--basis-commit-id manifest needs manifest.json")
		}
		basis = m.CommitID
	}

	type change struct {
		Path   string `json:"path"`
		Source string `json:"source"`
	}
	var changes []change
	var modified, added, unchanged []string
	seen := map[string]bool{}
	err = filepath.WalkDir(dir, func(p string, d fs.DirEntry, werr error) error {
		if werr != nil {
			return werr
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".nqe") {
			return nil
		}
		if d.Type()&fs.ModeSymlink != 0 {
			return fmt.Errorf("%s is a symbolic link; pack reads regular files only", p)
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		lp := "/" + strings.TrimSuffix(filepath.ToSlash(rel), ".nqe")
		if exportFileName(lp) == "" {
			return fmt.Errorf("%s does not map to a library path", p)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		seen[lp] = true
		sum := sha256.Sum256(b)
		switch e, ok := exported[lp]; {
		case !ok && haveManifest:
			added = append(added, lp)
		case ok && e.SHA256 != hex.EncodeToString(sum[:]):
			modified = append(modified, lp)
		case ok:
			unchanged = append(unchanged, lp)
			if o.changedOnly {
				return nil
			}
		}
		changes = append(changes, change{Path: lp, Source: string(b)})
		return nil
	})
	if err != nil {
		return fail(1, "%v", err)
	}
	if len(changes) == 0 {
		return fail(1, "no queries to pack: %s holds no .nqe files%s", dir, map[bool]string{true: " that differ from the manifest", false: ""}[o.changedOnly])
	}
	if len(changes) > skills.MaxQueryChanges {
		return fail(1, "%d queries, but one commit carries at most %d; pack a subtree (point pack at a subdirectory) or use --changed-only", len(changes), skills.MaxQueryChanges)
	}
	sort.Slice(changes, func(i, j int) bool { return changes[i].Path < changes[j].Path })
	in := map[string]any{"changes": changes}
	if o.message != "" {
		in["message"] = o.message
	}
	if o.createDirectory {
		in["create_directory"] = true
	}
	if o.typecheck {
		in["typecheck"] = true
	}
	if basis != "" {
		in["basis_commit_id"] = basis
	}
	b, _ := json.MarshalIndent(in, "", "  ")
	fmt.Fprintln(a.out, string(b))

	fmt.Fprintf(a.err, "packed %d quer%s from %s", len(changes), map[bool]string{true: "y", false: "ies"}[len(changes) == 1], dir)
	if haveManifest {
		sort.Strings(modified)
		sort.Strings(added)
		var missing []string
		for lp := range exported {
			if !seen[lp] {
				missing = append(missing, lp)
			}
		}
		sort.Strings(missing)
		fmt.Fprintf(a.err, " (exported at commit %s: %d modified, %d new, %d unchanged%s)", m.CommitID, len(modified), len(added), len(unchanged), map[bool]string{true: ", not re-sent", false: ""}[o.changedOnly])
		for _, p := range modified {
			fmt.Fprintf(a.err, "\n  modified %s", p)
		}
		for _, p := range added {
			fmt.Fprintf(a.err, "\n  new      %s", p)
		}
		if len(missing) > 0 {
			fmt.Fprintf(a.err, "\n  note: %d exported quer%s no longer in the tree (pack never deletes): %s", len(missing), map[bool]string{true: "y is", false: "ies are"}[len(missing) == 1], strings.Join(missing, ", "))
		}
	}
	fmt.Fprintln(a.err, "\nnext: review it with `fwdctl run edit-nqe-query < that.json` (a dry run), then add \"apply\": true after it is approved; data files the queries read are not part of a tree")
	return 0
}
