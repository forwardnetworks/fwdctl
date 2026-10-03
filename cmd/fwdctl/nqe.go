package main

import (
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/forwardnetworks/fwdctl/nqelint"
	"github.com/forwardnetworks/fwdctl/nqeschema"
)

// nqeCmd is the offline NQE checker: no Forward connection, no snapshot, milliseconds. It reads a query from FILE (or - for stdin)
// and reports syntax errors with line and column, deprecated constructs with the replacement, and field and enum names the data
// model does not have (with the closest real name). It does not type-check: validate-nqe-query asks Forward for that.
func nqeTool(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) > 0 && args[0] == "lsp" {
		if err := nqelint.ServeLSP(stdin, stdout); err != nil {
			fmt.Fprintf(stderr, "error: %v\n", err)
			return 1
		}
		return 0
	}
	if len(args) > 0 && args[0] == "fmt" {
		return nqeFormat(args[1:], stdin, stdout, stderr)
	}
	if len(args) > 0 && (args[0] == "complete" || args[0] == "hover") {
		return nqePosition(args, stdin, stdout, stderr)
	}
	if len(args) > 0 && args[0] == "template" {
		return nqeTemplate(args[1:], stdout, stderr)
	}
	synth, args := stringFlag(args, "--synthetic")
	mods, args := modulesFlag(args)
	if len(args) == 0 || args[0] != "lint" || len(args) > 2 {
		fmt.Fprintln(stderr, "usage: fwdctl nqe lint [--modules DIR] [FILE|-] | fmt [-w] [--check] [FILE...] | lsp | complete FILE LINE COL | hover FILE LINE COL")
		return usage
	}
	var src []byte
	var err error
	if len(args) == 2 && args[1] != "-" {
		src, err = os.ReadFile(args[1])
	} else {
		src, err = io.ReadAll(stdin)
	}
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return usage
	}
	if mods == nil && len(args) == 2 && args[1] != "-" { // imports are read from the files next to the query
		mods = nqelint.Dir(filepath.Dir(args[1]))
	} else if mods == nil {
		mods = nqelint.Modules
	}
	diags := nqelint.LintWith(string(src), mods)
	if synth != "" {
		more, err := nqelint.CheckSyntheticRows(string(src), synth)
		if err != nil {
			fmt.Fprintf(stderr, "error: %v\n", err)
			return usage
		}
		diags = append(diags, more...)
	}
	out := struct {
		Valid       bool                 `json:"valid"`
		Diagnostics []nqelint.Diagnostic `json:"diagnostics"`
		SchemaHints []nqeschema.Finding  `json:"schema_hints,omitempty"`
		Schema      map[string]any       `json:"schema,omitempty"`
		Note        string               `json:"note"`
	}{Valid: !nqelint.HasErrors(diags), Diagnostics: diags, Note: "offline: syntax, names, types and deprecations; the type check is gradual (silent where a type is unknown), so validate-nqe-query has the last word"}
	if out.Diagnostics == nil {
		out.Diagnostics = []nqelint.Diagnostic{}
	}
	out.Schema = lintSchema
	if m, err := nqeschema.Load(); err == nil {
		out.SchemaHints = m.Check(string(src))
	}
	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(out)
	if !out.Valid {
		return 1
	}
	return 0
}

// nqePosition answers a completion or hover question for a script or an agent: fwdctl nqe complete|hover FILE LINE COL (1-based).
func nqePosition(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) != 4 {
		fmt.Fprintf(stderr, "usage: fwdctl nqe %s FILE|- LINE COL   (1-based line and column)\n", args[0])
		return usage
	}
	var src []byte
	var err error
	if args[1] == "-" {
		src, err = io.ReadAll(stdin)
	} else {
		src, err = os.ReadFile(args[1])
	}
	line, e1 := strconv.Atoi(args[2])
	col, e2 := strconv.Atoi(args[3])
	if err != nil || e1 != nil || e2 != nil || line < 1 || col < 1 {
		fmt.Fprintln(stderr, "error: need a readable FILE and a 1-based LINE and COL")
		return usage
	}
	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	if args[0] == "complete" {
		items := nqelint.CompleteAtWith(string(src), line-1, col-1, fileMods(args[1]))
		if items == nil {
			items = []nqelint.CompletionItem{}
		}
		_ = enc.Encode(map[string]any{"items": items})
		return 0
	}
	h := nqelint.AnalyzeWith(string(src), fileMods(args[1])).HoverAt(line, col)
	if h == nil {
		_ = enc.Encode(map[string]any{"hover": nil})
		return 0
	}
	_ = enc.Encode(map[string]any{"hover": h.Markdown, "line": h.Span.Line, "column": h.Span.Col, "length": h.Span.EndCol - h.Span.Col})
	return 0
}

// nqeFormat lays queries out in the standard style: fwdctl nqe fmt [-w] [--check] [FILE ...]. With no file it filters stdin to stdout.
// -w rewrites the files; --check prints the files that would change and exits 1 if there are any.
func nqeFormat(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	write, check := false, false
	var files []string
	for _, a := range args {
		switch a {
		case "-w":
			write = true
		case "--check":
			check = true
		default:
			files = append(files, a)
		}
	}
	if len(files) == 0 {
		src, err := io.ReadAll(stdin)
		if err != nil {
			fmt.Fprintf(stderr, "error: %v\n", err)
			return usage
		}
		out, err := nqelint.Format(string(src))
		if err != nil {
			fmt.Fprintf(stderr, "error: %v\n", err)
			return 1
		}
		if check {
			if out != string(src) {
				return 1
			}
			return 0
		}
		fmt.Fprint(stdout, out)
		return 0
	}
	code := 0
	for _, f := range files {
		src, err := os.ReadFile(f)
		if err != nil {
			fmt.Fprintf(stderr, "error: %v\n", err)
			code = usage
			continue
		}
		out, err := nqelint.Format(string(src))
		if err != nil {
			fmt.Fprintf(stderr, "%s: %v\n", f, err)
			code = 1
			continue
		}
		changed := out != string(src)
		switch {
		case check:
			if changed {
				fmt.Fprintln(stdout, f)
				code = 1
			}
		case write:
			if changed {
				if err := os.WriteFile(f, []byte(out), 0o644); err != nil {
					fmt.Fprintf(stderr, "error: %v\n", err)
					code = 1
				}
			}
		default:
			fmt.Fprint(stdout, out)
		}
	}
	return code
}

// modulesFlag takes --modules DIR out of the arguments: where imports are read from (default: the directory of the file).
func modulesFlag(args []string) (nqelint.ModuleSource, []string) {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "--modules" {
			rest := append(append([]string{}, args[:i]...), args[i+2:]...)
			return nqelint.Dir(args[i+1]), rest
		}
	}
	return nil, args
}

func fileMods(file string) nqelint.ModuleSource {
	if file == "-" {
		return nqelint.Modules
	}
	return nqelint.Dir(filepath.Dir(file))
}

// stringFlag takes --name VALUE out of the arguments.
func stringFlag(args []string, name string) (string, []string) {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == name {
			return args[i+1], append(append([]string{}, args[:i]...), args[i+2:]...)
		}
	}
	return "", args
}

var templates embed.FS

var templateFor = map[string]string{"internet": "synthetic-internet", "intranet": "synthetic-internet", "l3vpn": "synthetic-l3vpn", "adjacent-network": "synthetic-l3vpn", "l2vpn": "synthetic-l2vpn"}

// nqeTemplate prints a starter query, the CLI's "add new query from template": fwdctl nqe template internet|intranet|l3vpn|adjacent-network|l2vpn.
func nqeTemplate(args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintf(stderr, "usage: fwdctl nqe template %s\n", strings.Join(nqelint.SyntheticKindNames(), "|"))
		return usage
	}
	name, ok := templateFor[args[0]]
	if !ok {
		fmt.Fprintf(stderr, "error: no template for %q; one of %s\n", args[0], strings.Join(nqelint.SyntheticKindNames(), ", "))
		return usage
	}
	b, _ := templates.ReadFile("templates/" + name + ".nqe")
	fmt.Fprint(stdout, string(b))
	return 0
}

// lintSchema is set by `nqe lint --org` to describe the live schema the check ran against (nil: the embedded one).
var lintSchema map[string]any
