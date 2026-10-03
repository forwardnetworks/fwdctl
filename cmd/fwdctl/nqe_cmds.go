package main

import (
	"fmt"
	"github.com/forwardnetworks/fwdctl/skills"
	"time"

	"github.com/spf13/cobra"

	"github.com/forwardnetworks/fwdctl/nqelint"
)

func (a *app) nqeCmd() *cobra.Command {
	c := parent(&cobra.Command{
		Use: "nqe", GroupID: "nqe", Short: "write, check, run and assemble NQE queries",
		Long: "NQE (Network Query Engine) tools. Offline, no Forward connection: lint, fmt, lsp, complete, hover, template. Connected: run, bundle, synthesize.\n" +
			"Before a connected command: FORWARD_URL, FORWARD_USERNAME, FORWARD_PASSWORD (or fwdctl login). Long queries: FORWARD_TIMEOUT, FORWARD_NQE_MODE, FORWARD_NQE_WAIT (fwdctl docs troubleshooting).",
	})
	c.AddCommand(a.nqeLint(), a.nqeFmt(), a.nqeLSP(), a.nqePos("complete", "what could be written at a position"), a.nqePos("hover", "what is at a position: its type and documentation"),
		a.nqeTemplate(), a.nqeRun(), a.nqeBundle(), a.nqeExport(), a.nqePack(), a.nqeSynth())
	return c
}

func (a *app) nqeLint() *cobra.Command {
	var modules, synthetic string
	var org bool
	c := &cobra.Command{
		Use: "lint [FILE|-]", Short: "check a query: syntax, names, types, deprecations (offline, or against an organization's live schema with --org)",
		Long: "Offline NQE check, no Forward connection: syntax errors with line and column, unknown names, wrong argument counts, fields and enum values the data model does not have,\n" +
			"type errors, and deprecations with Forward's own advice. Exit 1 on an error. The type check is gradual (it says nothing where it cannot tell a type), so\n" +
			"validate-nqe-query, which runs the query on Forward, is still the last word. An import of your own organization's saved query (not @fwd/...) warns rather than being\n" +
			"checked, since that library is per-organization and not sealed into this binary: `fwdctl nqe bundle` first for full coverage of it too.\n" +
			"Dead code is warned about, never an error (exit stays 0): an import none of whose names is used (unused-import), a parameter or let nothing reads (unused-param, unused-let) and a definition nothing reachable from the @query, the main\n" +
			"expression or an export refers to (unused-definition). Lint a `nqe bundle` to find what a whole module tree never uses; an exported definition is never called dead, since\n" +
			"another module may import it.\n" +
			"The embedded schema is a release's; an organization on a newer build has fields it lacks (and may have dropped fields it still has). --org reads the organization's live schema\n" +
			"(GET /api/nqe/schema; needs a login) and checks against that, so a field the organization no longer has is reported as an error, and the result says how many field paths the live schema\n" +
			"has that the embedded one lacks and the reverse (schema.source, fields_added, fields_removed).",
		Example: "  fwdctl nqe lint query.nqe\n  cat query.nqe | fwdctl nqe lint -",
		Args:    cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			tool := []string{}
			lintSchema = nil
			if org {
				sess, err := a.session()
				if err != nil {
					return a.fail("%v", err)
				}
				raw, err := sess.NQESchema(cmd.Context())
				if err != nil {
					return a.fail("could not read the organization's schema: %v", err)
				}
				added, removed, err := nqelint.UseOrgSchema(raw)
				if err != nil {
					return a.fail("could not use the organization's schema: %v", err)
				}
				lintSchema = map[string]any{"source": "org", "fields_added": added, "fields_removed": removed,
					"note": "fields_added are field paths the organization's schema has that this binary's embedded schema lacks; fields_removed are the reverse (a query using one of those fails on this organization)"}
			}
			if synthetic != "" {
				tool = append(tool, "--synthetic", synthetic)
			}
			if modules != "" {
				tool = append(tool, "--modules", modules)
			}
			return a.exit(nqeTool(append(append(tool, "lint"), args...), a.in, a.out, a.err))
		},
	}
	c.Flags().StringVar(&modules, "modules", "", "where \"import\" statements are read from (default: the directory of FILE)")
	c.Flags().BoolVar(&org, "org", false, "check against the organization's live schema instead of the embedded one (needs a login)")
	c.Flags().StringVar(&synthetic, "synthetic", "", "check the file as a synthetic-device query of this kind ("+join(nqelint.SyntheticKindNames())+")")
	_ = c.RegisterFlagCompletionFunc("synthetic", cobra.FixedCompletions(nqelint.SyntheticKindNames(), cobra.ShellCompDirectiveNoFileComp))
	return c
}

func (a *app) nqeFmt() *cobra.Command {
	var write, check bool
	c := &cobra.Command{
		Use: "fmt [FILE...]", Short: "format NQE in the standard style",
		Example: "  fwdctl nqe fmt -w queries/*.nqe\n  fwdctl nqe fmt --check queries/*.nqe",
		Long:    "Lay NQE out in the standard style. With no file it filters stdin to stdout. -w rewrites the files; --check prints the files that would change and exits 1 if any would.",
		RunE: func(cmd *cobra.Command, args []string) error {
			var tool []string
			if write {
				tool = append(tool, "-w")
			}
			if check {
				tool = append(tool, "--check")
			}
			return a.exit(nqeTool(append([]string{"fmt"}, append(tool, args...)...), a.in, a.out, a.err))
		},
	}
	c.Flags().BoolVarP(&write, "write", "w", false, "rewrite the files in place")
	c.Flags().BoolVar(&check, "check", false, "list the files that would change and exit 1 if any would")
	return c
}

func (a *app) nqeLSP() *cobra.Command {
	return &cobra.Command{
		Use: "lsp", Short: "a language server for NQE (editors: diagnostics, completion, hover, quick fixes)",
		Example: "  fwdctl nqe lsp   # an editor launches this over stdin/stdout",
		Long:    "A language server for NQE over stdin/stdout (diagnostics, completion, hover, quick fixes), for an editor to launch.",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.exit(nqeTool([]string{"lsp"}, a.in, a.out, a.err))
		},
	}
}

func (a *app) nqePos(name, short string) *cobra.Command {
	return &cobra.Command{
		Use: name + " FILE|- LINE COL", Short: short + " (1-based line and column)", Args: cobra.ExactArgs(3),
		Example: "  fwdctl nqe " + name + " query.nqe 3 12",
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.exit(nqeTool(append([]string{name}, args...), a.in, a.out, a.err))
		},
	}
}

func (a *app) nqeTemplate() *cobra.Command {
	kinds := nqelint.SyntheticKindNames()
	return &cobra.Command{
		Use: "template KIND", Short: "a starter query for a synthetic device (" + join(kinds) + ")", Args: cobra.ExactArgs(1), ValidArgs: kinds,
		Example: "  fwdctl nqe template internet > internet.nqe",
		Long:    "Print a starter query for a synthetic device (the CLI's \"add new query from template\").",
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.exit(nqeTool([]string{"template", args[0]}, a.in, a.out, a.err))
		},
	}
}

func (a *app) nqeRun() *cobra.Command {
	var o nqeRunOpts
	c := &cobra.Command{
		Use: "run", Short: "run a query (a file, stdin or a saved one at a commit) and print every row, or one page",
		Long: `Run an NQE query on Forward and print the rows (stdout), with timing and scope on stderr. Every row is fetched, paged for you (up to --max), or with --limit/--offset ONE page.
A saved library query runs by id at a commit. --async uses Forward's asynchronous execution API, --meta records how the run went. A synchronous call that is cut off by the HTTP
timeout falls back to the asynchronous API by itself (FORWARD_NQE_MODE=sync turns that off, =async always uses it; FORWARD_NQE_WAIT bounds the wait).
Exit status: 0 ok, 1 the query does not compile, 2 no processed snapshot, 3 error, 64 bad usage.`,
		Example: `  fwdctl nqe run --network 123 --file q.nqe --format table
  fwdctl nqe run --network 123 --query-id Q_abc --commit-id 9f3c --param threshold=10 --meta run.json
  fwdctl nqe run --network 123 --file q.nqe --offset 200 --limit 100 --meta page.json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if o.network == "" {
				return a.fail("--network is required (the network id; `echo '{}' | fwdctl run inspect-networks` lists them)")
			}
			if !validFormat(o.format) {
				return a.fail("--format is json, jsonl, table or csv")
			}
			if o.queryID == "" && o.commitID != "" {
				return a.fail("--commit-id needs --query-id")
			}
			requestTimeout = o.waitMax
			return a.exit(nqeRunCmd(a, o))
		},
	}
	f := c.Flags()
	f.StringVar(&o.network, "network", "", "network id (required)")
	f.StringVar(&o.file, "file", "", "file with the NQE query (default: stdin)")
	f.StringVar(&o.snapshot, "snapshot", "", "snapshot id (default: the latest processed)")
	f.StringVar(&o.format, "format", "json", "json, jsonl, table or csv")
	f.StringVar(&o.countBy, "count-by", "", "print how many rows have each value of this field instead of the rows")
	f.IntVar(&o.max, "max", 50000, "stop after this many rows (stated on stderr when more exist)")
	f.StringVar(&o.queryID, "query-id", "", "run a saved query by id instead of a file (a library query: see fwdctl run find-nqe-query)")
	f.StringVar(&o.commitID, "commit-id", "", "with --query-id: the library commit to run it at (default: the head)")
	f.StringVar(&o.paramsFile, "params", "", "JSON file with the query's parameters, an object of name to typed value")
	f.StringArrayVar(&o.params, "param", nil, "one parameter as NAME=JSON (repeatable; a value that is not JSON is a string), overrides --params")
	f.BoolVar(&o.allowLarge, "allow-large", false, "write a result of more than 100 MB (by default it is refused before anything is written, so a redirect cannot fill the disk)")
	f.BoolVar(&o.asyncRun, "async", false, "run through Forward's asynchronous execution API (the execution key and outcome are in --meta)")
	f.StringVar(&o.metaOut, "meta", "", "write a JSON object about the run (mode, execution key, outcome, Forward's execution time, rows, HTTP status and diagnostics on failure) to this file, or - for stderr")
	f.IntVar(&o.pageOffset, "offset", 0, "read one page: skip this many rows (with --limit; the page, the total and the offset are in --meta)")
	f.IntVar(&o.pageLimit, "limit", 0, "read one page of at most this many rows instead of every row (0: every row, up to --max)")
	f.DurationVar(&o.waitMax, "timeout", 10*time.Minute, "how long to wait: the whole synchronous request (response included; the default HTTP limit is 120s), or with --async the execution")
	c.MarkFlagsMutuallyExclusive("file", "query-id")
	_ = c.RegisterFlagCompletionFunc("format", cobra.FixedCompletions([]string{"json", "jsonl", "table", "csv"}, cobra.ShellCompDirectiveNoFileComp))
	_ = c.RegisterFlagCompletionFunc("network", a.completeNetworks)
	_ = c.RegisterFlagCompletionFunc("snapshot", a.completeSnapshots)
	return c
}

func (a *app) nqePack() *cobra.Command {
	var o nqePackOpts
	c := &cobra.Command{
		Use: "pack DIR|UI.zip", Short: "turn a folder tree of .nqe files into the input of edit-nqe-query, to commit it to a library as ONE commit",
		Long: `Read DIR (as nqe export writes it: <library path>.nqe files, with manifest.json when there is one) and print the edit-nqe-query input that commits the tree in ONE commit:
{"changes": [{"path", "source"}...]}. It only reads the directory and prints JSON; the write is edit-nqe-query's own dry run, so pipe the file to "fwdctl run edit-nqe-query", read the plan,
then add "apply": true. With a manifest, stderr says which files changed since the export, which are new and which are gone (pack never deletes), and --changed-only sends just the edited
ones. A zip in the Forward UI's export format (it holds queries-export.proto) is read too, so a package the UI's Export produced can be committed with fwdctl; it has no manifest, so there is no change report. --create-directory makes missing library folders in the same commit (needed to load a tree into a library that lacks them); --typecheck has Forward type every query and every
importer first; --basis-commit-id C (or "manifest" for the export's commit) refuses the commit if the library head is not C. A commit carries at most ` + fmt.Sprint(skills.MaxQueryChanges) + ` queries.`,
		Example: `  fwdctl nqe pack tree/ --message "Load parser modules" --create-directory --typecheck > load.json
  fwdctl run edit-nqe-query < load.json                       # the dry run: the plan, the lint, Forward's typecheck
  fwdctl nqe pack tree/ --changed-only --basis-commit-id manifest > edits.json   # push only what was edited since the export`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error { return a.exit(nqePackCmd(a, args[0], o)) },
	}
	f := c.Flags()
	f.StringVar(&o.message, "message", "", "the commit title")
	f.BoolVar(&o.createDirectory, "create-directory", false, "create missing library directories in the same commit")
	f.BoolVar(&o.typecheck, "typecheck", false, "have Forward typecheck the changed queries and their importers before committing")
	f.StringVar(&o.basis, "basis-commit-id", "", `refuse to commit if the library head is not this commit ("manifest": the commit the tree was exported at)`)
	f.BoolVar(&o.changedOnly, "changed-only", false, "send only files that differ from manifest.json or are not in it")
	return c
}

func (a *app) nqeExport() *cobra.Command {
	var o nqeExportOpts
	c := &cobra.Command{
		Use: "export", Short: "write the entry and every library module it imports to a folder tree that mirrors the library",
		Long: `Write the entry query and every organization library module it imports, transitively, to a folder tree: one <query name>.nqe per query under the library path
(/Team/Sub/Mod is DIR/Team/Sub/Mod.nqe), import statements left exactly as they are (they already name library paths, so the tree re-imports as it is), and manifest.json with each
file's library path, query id, commit id, sha256 and imports. Unlike bundle it inlines nothing. The tree and --zip are fwdctl's own format: the Forward UI's library import refuses them
("missing file queries-export.proto"); --ui-zip writes the package the UI's Export produces and Import reads (checked against a package Forward itself exported: the same queries with identical sources; Forward lists them in an arbitrary order, so two exports differ in order only). Imports of @fwd/... are listed, not fetched. The data files the queries read
(network.extensions.<name>) are reported in the manifest and on stderr: they are organization uploads, not part of the export. --override and --add-module behave as in bundle, and
one that matches nothing is an error. DIR must be empty (or pass --force); --zip also writes the same files as a zip.`,
		Example: `  fwdctl nqe export --path "/Team/Entry" --commit-id 9f3c --out export/
  fwdctl nqe export --query-id Q_abc --out export/ --zip export.zip
  fwdctl nqe export --query-id Q_abc --out export/ --ui-zip for-the-ui.zip   # what the Forward UI imports`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error { return a.exit(nqeExportCmd(a, o)) },
	}
	f := c.Flags()
	f.StringVar(&o.queryID, "query-id", "", "the entry query, by id (its library path is read from the head listing)")
	f.StringVar(&o.path, "path", "", "the entry query, by library path")
	f.StringVar(&o.commit, "commit-id", "", "the library commit to read at (default: the head, recorded in the manifest)")
	f.StringVar(&o.out, "out", "", "the directory to write (required)")
	f.StringVar(&o.zipFile, "zip", "", "also write the tree and manifest as this zip (fwdctl's own format: the Forward UI cannot import it)")
	f.StringVar(&o.uiZip, "ui-zip", "", "also write the queries in the Forward UI's library import format (a zip holding queries-export.proto)")
	f.BoolVar(&o.force, "force", false, "write into a directory that is not empty")
	f.StringArrayVar(&o.overrides, "override", nil, "LIBRARY_PATH=FILE: use this local file instead of the module (or the entry) at that path; repeatable")
	f.StringArrayVar(&o.added, "add-module", nil, "LIBRARY_PATH=FILE: a module that exists only locally; repeatable")
	c.MarkFlagsMutuallyExclusive("query-id", "path")
	c.MarkFlagsOneRequired("query-id", "path")
	_ = c.MarkFlagRequired("out")
	return c
}

func (a *app) nqeBundle() *cobra.Command {
	var o nqeBundleOpts
	c := &cobra.Command{
		Use: "bundle", Short: "print ONE self-contained query: an entry and every library module it imports at a commit",
		Long: `Print ONE self-contained query: the entry and every library module it imports at a commit, names prefixed per module, local files substituted. Inline query text always imports
against the library head and a commit cannot be combined with inline text, so testing an older or an uncommitted version of a module needs this. An --override of the entry's own
path replaces the entry; an --override or --add-module the bundle never reads is an error. Run the result with: fwdctl nqe run --network ID --file FILE`,
		Example: `  fwdctl nqe bundle --path "/Team/Entry" --commit-id 9f3c --out pre.nqe
  fwdctl nqe bundle --query-id Q_abc --override "/Team/Helpers=helpers.nqe" --out post.nqe`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error { return a.exit(nqeBundleCmd(a, o)) },
	}
	f := c.Flags()
	f.StringVar(&o.queryID, "query-id", "", "the entry query, by id")
	f.StringVar(&o.path, "path", "", "the entry query, by library path")
	f.StringVar(&o.commit, "commit-id", "", "the library commit to read modules at (default: the head)")
	f.StringVar(&o.out, "out", "", "write the bundle to this file (default: stdout)")
	f.StringArrayVar(&o.overrides, "override", nil, "LIBRARY_PATH=FILE: use this local file instead of the module (or the entry) at that path; repeatable")
	f.StringArrayVar(&o.added, "add-module", nil, "LIBRARY_PATH=FILE: a module that exists only locally, importable by the entry or an override; repeatable")
	c.MarkFlagsMutuallyExclusive("query-id", "path")
	c.MarkFlagsOneRequired("query-id", "path")
	return c
}

func (a *app) nqeSynth() *cobra.Command {
	c := parent(&cobra.Command{
		Use: "synthesize", Short: "derive a synthetic device's query from the model (internet)",
		Long: "Derive a synthetic-device query from evidence in the network model, lint it and print it; exit 1 if its own output is not clean.",
	})
	var o nqeSynthOpts
	internet := &cobra.Command{
		Use: "internet", Short: "derive an internet node's connection query (the inspect-edge analysis)", Args: cobra.NoArgs,
		Long:    "Derive an internet node's connection query from the model (the inspect-edge analysis), lint it and print it; exit 1 if its own output is not clean.",
		Example: "  fwdctl nqe synthesize internet --network 123 --vrf default",
		RunE:    func(cmd *cobra.Command, args []string) error { return a.exit(nqeSynthesizeCmd(a, o)) },
	}
	f := internet.Flags()
	f.StringVar(&o.network, "network", "", "network id (required)")
	f.StringVar(&o.vrf, "vrf", "", "only default routes in this VRF")
	f.StringVar(&o.device, "device", "", "only default routes on this device")
	f.StringVar(&o.discovery, "discovery", "interfaceAddresses", "interfaceAddresses, bgpRoutes, ipRoutes or none")
	f.StringVar(&o.subnets, "subnets", "", "comma-separated prefixes written on every row (required for --discovery none)")
	f.BoolVar(&o.unlikely, "include-unlikely", false, "also write rows for unowned exits that are not likely internet edges")
	f.StringVar(&o.snapshot, "snapshot", "", "snapshot id (default: the latest processed)")
	_ = internet.MarkFlagRequired("network")
	_ = internet.RegisterFlagCompletionFunc("network", a.completeNetworks)
	_ = internet.RegisterFlagCompletionFunc("snapshot", a.completeSnapshots)
	_ = internet.RegisterFlagCompletionFunc("discovery", cobra.FixedCompletions([]string{"interfaceAddresses", "bgpRoutes", "ipRoutes", "none"}, cobra.ShellCompDirectiveNoFileComp))
	c.AddCommand(internet)
	return c
}

func join(s []string) string {
	out := ""
	for i, v := range s {
		if i > 0 {
			out += "|"
		}
		out += v
	}
	return out
}
