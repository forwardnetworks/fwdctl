package main

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/forwardnetworks/fwdctl/skills"
)

// cmdClass classifies a fwdctl command for a host that exposes fwdctl (an MCP server, a copilot): what it does to Forward and to the machine it runs on. A command with no entry here fails
// TestEveryCommandHasAClassInTheManifest, so a new command cannot ship unclassified.
//
//	class: "local"        no Forward connection: it reads or writes nothing on Forward
//	       "read"         asks Forward and changes nothing on Forward
//	       "write"        changes something on Forward (dry run unless apply is given, where the command says so)
//	       "local-write"  changes the machine it runs on (a config, an installed file, the binary)
//	       "runner"       dispatches a named skill: the skill's own class (see the skills list) applies
//	       "group"        only names subcommands
type cmdClass struct {
	Class       string // see above
	WritesLocal bool   // writes files on this machine (an output directory, a downloaded file, an installed config)
	Secrets     bool   // its output or the files it writes can contain secrets (device files, credentials); redaction is best effort
	Note        string
}

var commandClasses = map[string]cmdClass{
	"collector":               {Class: "group"},
	"commands":                {Class: "local", Note: "this manifest; offline"},
	"collector download":      {Class: "read", WritesLocal: true, Note: "downloads the collector installer to a local file"},
	"context":                 {Class: "group"},
	"context nqe":             {Class: "local", Note: "NQE language help; offline"},
	"context schema":          {Class: "local", Note: "the embedded data-model schema; offline"},
	"describe":                {Class: "local", Note: "prints a skill's instructions; offline"},
	"device-file":             {Class: "group"},
	"device-file download":    {Class: "read", WritesLocal: true, Secrets: true, Note: "one collected device file to a local file; secret values on a keyword line are redacted by default (best effort), --no-redact writes it as collected"},
	"device-file export":      {Class: "read", WritesLocal: true, Secrets: true, Note: "the collected files of a snapshot's devices to a NEW local directory (mode 700, files 600) with manifest.json; read-only on Forward; redaction is line-based and best effort and does not see a secret inside an XML or JSON element; --dry-run lists and writes nothing; exit 1 when any file failed or was too large"},
	"docs":                    {Class: "local"},
	"install":                 {Class: "group"},
	"install agent-skills":    {Class: "local-write", WritesLocal: true, Note: "writes the skills into an agent-skills directory"},
	"install agents":          {Class: "local-write", WritesLocal: true},
	"install claude":          {Class: "local-write", WritesLocal: true},
	"install lsp":             {Class: "local-write", WritesLocal: true},
	"list":                    {Class: "local", Note: "lists the skills; offline"},
	"man":                     {Class: "local", Note: "manual pages"},
	"login":                   {Class: "local-write", WritesLocal: true, Secrets: true, Note: "saves a connection (host and the path of a token file) in the local config; reads a token file"},
	"nqe":                     {Class: "group"},
	"nqe bundle":              {Class: "read", Note: "reads library modules from Forward and prints one self-contained query"},
	"nqe compare":             {Class: "read", Note: "runs two versions of a query on one snapshot; every run is a real execution"},
	"nqe complete":            {Class: "local"},
	"nqe diff":                {Class: "read", Note: "runs a query on two snapshots"},
	"nqe explain":             {Class: "write", Note: "with --apply it saves a throwaway draft query in the user's NQE workspace, explains it and discards it (dry run otherwise)"},
	"nqe export":              {Class: "read", WritesLocal: true, Note: "writes library queries to a local folder tree"},
	"nqe features":            {Class: "read", Note: "lists Forward's NQE feature flags for the organization; offline listing without a login"},
	"nqe fmt":                 {Class: "local", Note: "-w rewrites local files"},
	"nqe hover":               {Class: "local"},
	"nqe lint":                {Class: "local", Note: "offline; with --org it reads the organization's live schema"},
	"nqe lsp":                 {Class: "local"},
	"nqe metrics":             {Class: "read"},
	"nqe pack":                {Class: "local", Note: "turns a local folder into edit-nqe-query input"},
	"nqe params":              {Class: "local"},
	"nqe run":                 {Class: "read", Note: "runs an NQE query; a saved query by id or an inline one; changes nothing on Forward"},
	"nqe synthesize":          {Class: "group"},
	"nqe synthesize internet": {Class: "read", Note: "derives a synthetic device's query from the model"},
	"nqe template":            {Class: "local"},
	"run":                     {Class: "runner", Note: "runs a named skill with a JSON object on stdin; a write skill is a dry run unless its input says apply:true"},
	"source":                  {Class: "group"},
	"source apply":            {Class: "write", Secrets: true, Note: "creates many sources from one file; dry run unless --apply; secrets come from files"},
	"update":                  {Class: "local-write", WritesLocal: true, Note: "replaces the fwdctl binary"},
	"version":                 {Class: "local"},
	"wait":                    {Class: "group"},
	"wait collector":          {Class: "read", Note: "polls"},
	"wait snapshot":           {Class: "read", Note: "polls"},
	"which":                   {Class: "local", Note: "the offline router: which skill answers a question"},
	"whoami":                  {Class: "read", Note: "who the saved connection is and what Forward it reaches"},
}

type manifestCommand struct {
	Path         string `json:"path"`
	Class        string `json:"class"`
	WritesLocal  bool   `json:"writes_local_files,omitempty"`
	MaySecrets   bool   `json:"output_may_contain_secrets,omitempty"`
	Note         string `json:"note,omitempty"`
	Short        string `json:"short,omitempty"`
	Unclassified bool   `json:"unclassified,omitempty"`
}

type manifestSkill struct {
	Name       string `json:"name"`
	Class      string `json:"class"`
	Effect     string `json:"effect,omitempty"`
	Reversible bool   `json:"reversible,omitempty"`
	Secrets    bool   `json:"secrets"`
	Runnable   bool   `json:"runnable"`
}

type commandsManifest struct {
	Version  string            `json:"fwdctl_version"`
	Commands []manifestCommand `json:"commands"`
	Skills   []manifestSkill   `json:"skills"`
}

// buildCommandsManifest lists every command of the tree with its class, and every skill with its class, effect and secrets flag: what a host needs to decide which of them it exposes and how.
func buildCommandsManifest(root *cobra.Command) (commandsManifest, error) {
	var paths [][]string
	var walkAll func(c *cobra.Command, p []string)
	walkAll = func(c *cobra.Command, p []string) {
		for _, sub := range c.Commands() {
			if sub.Name() == "help" || sub.Name() == "completion" {
				continue
			}
			np := append(append([]string{}, p...), sub.Name())
			paths = append(paths, np)
			walkAll(sub, np)
		}
	}
	walkAll(root, nil)
	m := commandsManifest{Version: version}
	byPath := map[string]*cobra.Command{}
	var find func(c *cobra.Command, p []string)
	find = func(c *cobra.Command, p []string) {
		for _, sub := range c.Commands() {
			np := append(append([]string{}, p...), sub.Name())
			byPath[strings.Join(np, " ")] = sub
			find(sub, np)
		}
	}
	find(root, nil)
	for _, p := range paths {
		key := strings.Join(p, " ")
		c := byPath[key]
		mc := manifestCommand{Path: key, Short: c.Short}
		if cl, ok := commandClasses[key]; ok {
			mc.Class, mc.WritesLocal, mc.MaySecrets, mc.Note = cl.Class, cl.WritesLocal, cl.Secrets, cl.Note
		} else {
			mc.Unclassified = true
		}
		m.Commands = append(m.Commands, mc)
	}
	sort.Slice(m.Commands, func(i, j int) bool { return m.Commands[i].Path < m.Commands[j].Path })
	all, err := skills.All()
	if err != nil {
		return m, err
	}
	for _, s := range all {
		m.Skills = append(m.Skills, manifestSkill{Name: s.Name, Class: s.Class, Effect: s.Effect, Reversible: s.Reversible, Secrets: s.Secrets, Runnable: s.Runnable})
	}
	sort.Slice(m.Skills, func(i, j int) bool { return m.Skills[i].Name < m.Skills[j].Name })
	return m, nil
}

func (a *app) commandsCmd() *cobra.Command {
	return &cobra.Command{
		Use: "commands", GroupID: "setup", Short: "a machine-readable list of every command and skill with its class (read, write, local)", Args: cobra.NoArgs,
		Example: "  fwdctl commands > fwdctl-commands.json",
		Long:    "Print JSON: every fwdctl command with its class (local: no Forward connection; read: asks Forward and changes nothing; write: changes Forward; local-write: changes this machine; runner: runs a named skill; group), whether it writes local files and whether its output can contain secrets, and every skill with its class (read or write), effect (network, snapshot or org), reversibility and secrets flag. For a host that exposes fwdctl and must decide how to classify each command: diff this against its own mapping. Offline.",
		RunE: func(cmd *cobra.Command, args []string) error {
			m, err := buildCommandsManifest(cmd.Root())
			if err != nil {
				fmt.Fprintf(a.err, "error: %v\n", err)
				return a.exit(3)
			}
			b, _ := json.MarshalIndent(m, "", "  ")
			fmt.Fprintln(a.out, string(b))
			return nil
		},
	}
}
