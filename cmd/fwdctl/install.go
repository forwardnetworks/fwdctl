package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/forwardnetworks/fwdctl/skills"
)

const (
	beginMarker = "<!-- forward-skills:begin (managed by `fwdctl install agents`; edits inside are overwritten) -->"
	endMarker   = "<!-- forward-skills:end -->"
)

// yamlString quotes s as a YAML double-quoted scalar. JSON string syntax is valid YAML, and it keeps the colons
// and quotes in a description from breaking the frontmatter.
func yamlString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

type schemaProp struct {
	Type        any    `json:"type"`
	Enum        []any  `json:"enum"`
	Pattern     string `json:"pattern"`
	Description string `json:"description"`
	Default     any    `json:"default"`
}

// inputTable renders a skill's input schema as a markdown table.
func inputTable(schema json.RawMessage) string {
	var s struct {
		Required   []string              `json:"required"`
		Properties map[string]schemaProp `json:"properties"`
	}
	if json.Unmarshal(schema, &s) != nil || len(s.Properties) == 0 {
		return ""
	}
	req := map[string]bool{}
	for _, r := range s.Required {
		req[r] = true
	}
	names := make([]string, 0, len(s.Properties))
	for n := range s.Properties {
		names = append(names, n)
	}
	sort.Slice(names, func(i, j int) bool {
		if req[names[i]] != req[names[j]] {
			return req[names[i]]
		}
		return names[i] < names[j]
	})
	var b strings.Builder
	b.WriteString("| Input | Type | Required | Notes |\n|---|---|---|---|\n")
	for _, n := range names {
		p := s.Properties[n]
		t := fmt.Sprint(p.Type)
		if len(p.Enum) > 0 {
			vals := make([]string, len(p.Enum))
			for i, v := range p.Enum {
				vals[i] = fmt.Sprint(v)
			}
			t = strings.Join(vals, " \\| ")
		}
		if arr, ok := p.Type.([]any); ok {
			parts := make([]string, len(arr))
			for i, v := range arr {
				parts[i] = fmt.Sprint(v)
			}
			t = strings.Join(parts, " or ")
		}
		yes := ""
		if req[n] {
			yes = "yes"
		}
		note := strings.ReplaceAll(strings.TrimSpace(p.Description), "|", "\\|")
		if p.Default != nil {
			if note != "" {
				note += " "
			}
			note += fmt.Sprintf("(default %v)", p.Default)
		}
		fmt.Fprintf(&b, "| `%s` | %s | %s | %s |\n", n, t, yes, note)
	}
	return b.String()
}

func runSection(m skills.Meta) string {
	var b strings.Builder
	b.WriteString("\n## Running this skill\n\n")
	fmt.Fprintf(&b, "Run it with the `fwdctl` command, giving the inputs as one JSON object on stdin:\n\n```bash\necho '%s' | fwdctl run %s\n```\n\n", exampleInput(m.InputSchema), m.Name)
	if t := inputTable(m.InputSchema); t != "" {
		b.WriteString(t + "\n")
	}
	if m.Class == "write" {
		b.WriteString("**This skill changes Forward.** It is a dry run unless the input has `\"apply\": true`: the result then lists each planned change (`mode` `dry_run`, `changes`) and changes nothing. Read the plan first; apply only what you mean to. An applied result lists what was done and how to undo it.\n\n")
	}
	b.WriteString("It needs `FORWARD_URL`, `FORWARD_USERNAME` and `FORWARD_PASSWORD` in the environment (an API token's access key and secret work).\n\n")
	b.WriteString("It prints one JSON result: `status` is `ok`, `failed`, `unknown` or `error`, with `evidence`, `limits` (what was NOT measured) and `next_actions`. ")
	b.WriteString("The exit code is 0 ok, 1 failed, 2 unknown, 3 error. **`unknown` is never a pass**: read the `limits` before relying on a result.\n")
	return b.String()
}

// claudeSkill is the SKILL.md for Claude Code: only the frontmatter keys it reads, the procedure, and how to run it.
func claudeSkill(m skills.Meta) string {
	var b strings.Builder
	fmt.Fprintf(&b, "---\nname: %s\ndescription: %s\n", m.Name, yamlString(m.Description))
	if m.Compatibility != "" {
		fmt.Fprintf(&b, "compatibility: %s\n", yamlString(m.Compatibility))
	}
	b.WriteString("---\n\n")
	body := m.Body
	// The source SKILL.md carries a short run footer for plugin installs; the CLI writes a richer one with the inputs table.
	if i := strings.Index(body, "\n## Running this skill"); i >= 0 {
		body = body[:i]
	}
	b.WriteString(strings.TrimRight(body, "\n") + "\n")
	if m.Runnable {
		b.WriteString(runSection(m))
	}
	return b.String()
}

// installClaude writes every skill under dir as <name>/SKILL.md.
func installClaude(dir string, all []skills.Meta) ([]string, error) {
	var written []string
	for _, m := range all {
		d := filepath.Join(dir, m.Name)
		if err := os.MkdirAll(d, 0o755); err != nil {
			return written, err
		}
		p := filepath.Join(d, "SKILL.md")
		if err := os.WriteFile(p, []byte(claudeSkill(m)), 0o644); err != nil {
			return written, err
		}
		written = append(written, p)
		for _, ref := range m.References {
			text, err := skills.Reference(m.Name, ref)
			if err != nil {
				return written, err
			}
			rp := filepath.Join(d, filepath.FromSlash(ref))
			if err := os.MkdirAll(filepath.Dir(rp), 0o755); err != nil {
				return written, err
			}
			if err := os.WriteFile(rp, []byte(text), 0o644); err != nil {
				return written, err
			}
		}
	}
	return written, nil
}

// agentsBlock is the section for an AGENTS.md / CLAUDE.md style instruction file: how to work with the skills, and every skill's
// name with a few words, grouped by area. The descriptions are NOT repeated here (a plugin or `fwdctl list` already carries them, and
// every line of this block is spent in every conversation); the procedures stay in the skills, which the agent loads by name.
func agentsBlock(all []skills.Meta) string {
	var b strings.Builder
	b.WriteString(beginMarker + "\n## Forward Skills\n\n")
	b.WriteString(agentsRules)
	b.WriteString("\n### Skills by area\n\n")
	b.WriteString("Router: `plan-investigation` (read first). `plan-*` are playbooks (`fwdctl describe <name>`; they do not run). `edit-*` change Forward's own data (dry run first; read `plan-safe-write`). The rest only read.\n\n")
	for _, c := range skills.Clusters {
		var items []string
		for _, m := range all {
			if m.Cluster == c.Name {
				items = append(items, fmt.Sprintf("`%s` (%s)", m.Name, m.Summary))
			}
		}
		if len(items) > 0 {
			fmt.Fprintf(&b, "- **%s**: %s\n", c.Title, strings.Join(items, ", "))
		}
	}
	b.WriteString(endMarker + "\n")
	return b.String()
}

// agentsRules is how an AI is told to work with the skills: the same text is in `fwdctl docs agents`, so an agent that was given no
// instruction file can still read it. Keep it short: every line here is spent in every conversation.
const agentsRules = `Forward Networks skills for network questions. Most read Forward's digital twin and return evidence; the ` + "`edit-*`" + ` skills change Forward's own data (never a device).
Run one with ` + "`echo '<json inputs>' | fwdctl run <skill>`" + ` (needs ` + "`FORWARD_URL`, `FORWARD_USERNAME`, `FORWARD_PASSWORD`" + `). List them with ` + "`fwdctl list`" + `, read a skill's procedure with ` + "`fwdctl describe <skill>`" + `, its inputs with ` + "`fwdctl run <skill> --help`" + `.

**How to work**

0. **Not sure which skill?** ` + "`fwdctl which \"<the question>\"`" + ` ranks the router table offline and names the skill or playbook.
1. **Start with a playbook when the question is a task.** ` + "`fwdctl describe plan-investigation`" + ` maps the question to a playbook (troubleshooting, security, change, audit, health) or to one skill. Follow the playbook's order and stop when the evidence answers the question.
2. **Read the result, not just the answer.** ` + "`status`" + ` is ok, failed, unknown or error: **unknown is never a pass**. Read ` + "`limits`" + ` (what was not measured) and ` + "`context`" + ` (which snapshot and when) before you rely on it, and say them. ` + "`next_actions`" + ` names the skill that usually follows.
3. **Never improvise a write.** An ` + "`edit-*`" + ` skill is a dry run until its input has ` + "`apply: true`" + `. Run the plan, show the person the exact change and its undo, and apply only after they approve that plan. ` + "`fwdctl describe plan-safe-write`" + ` has the protocol.
4. **Recommend, never reconfigure.** No skill pushes configuration to a device. Say what to change; do not claim you changed it.
5. **Exit codes:** 0 ok, 1 failed (a finding), 2 unknown, 3 error, 64 bad usage.
6. **Keep the context small.** After each result write one line (what it showed, the snapshot, what is still unknown) and carry that forward instead of the raw evidence; ask for the page or the filter you need (` + "`limit`" + `, ` + "`view`" + `, a device) rather than everything. For a long investigation keep a short running findings list, and where you can delegate, give each independent branch (one device, one flow, one CVE) to a sub-agent and ask it to return only its conclusion with the snapshot and the evidence it rests on.
7. **DOGFOOD-TEMP:** if a forward-skills result is wrong, misleading or missing something you needed, finish the task, then read ` + "`plan-report-skill-gap`" + ` and offer to file a redacted GitHub issue; run ` + "`fwdctl redact-check --file <draft> --deny <each customer, organization or network name you know>`" + ` until it exits 0 (exit 2 = fix or ask the engineer) before showing it, and keep real customer specifics in a local ` + "`fwdctl dogfood-note`" + ` file, never in the issue. Skip all of this when ` + "`FWDCTL_DOGFOOD=off`" + `.
`

// mergeBlock puts block into existing: it replaces a previous managed block, or appends. Everything outside the
// markers is left exactly as it was.
func mergeBlock(existing, block string) string {
	i := strings.Index(existing, "<!-- forward-skills:begin")
	j := strings.Index(existing, endMarker)
	if i >= 0 && j > i {
		return existing[:i] + block + strings.TrimPrefix(existing[j+len(endMarker):], "\n")
	}
	if strings.TrimSpace(existing) == "" {
		return block
	}
	return strings.TrimRight(existing, "\n") + "\n\n" + block
}

func installAgents(file string, all []skills.Meta) error {
	block := agentsBlock(all)
	var existing string
	if b, err := os.ReadFile(file); err == nil {
		existing = string(b)
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return err
	}
	return os.WriteFile(file, []byte(mergeBlock(existing, block)), 0o644)
}

// exampleInput is a runnable-looking JSON object for a skill: its required inputs with a placeholder of the right kind (the first allowed value for an enumeration), or the
// network id when nothing is required and the skill takes one.
func exampleInput(schema json.RawMessage) string {
	var s struct {
		Required   []string              `json:"required"`
		Properties map[string]schemaProp `json:"properties"`
	}
	if json.Unmarshal(schema, &s) != nil {
		return "{}"
	}
	names := s.Required
	if len(names) == 0 {
		// a skill that takes a network gets the network id; one whose network_id has its own note (a window's snapshot, say) is not about a network, so {} is the example
		if p, ok := s.Properties["network_id"]; ok && p.Description == "" {
			names = []string{"network_id"}
		}
	}
	parts := make([]string, 0, len(names))
	for _, n := range names {
		p := s.Properties[n]
		var v string
		switch {
		case len(p.Enum) > 0:
			v = fmt.Sprintf("%q", fmt.Sprint(p.Enum[0]))
		case p.Type == "boolean":
			v = "true"
		case p.Type == "integer" || p.Type == "number":
			v = "1"
		case p.Type == "array":
			v = "[]"
		case p.Type == "object":
			v = "{}"
		case n == "network_id":
			v = `"<network id>"`
		default:
			v = fmt.Sprintf("%q", "<"+strings.ReplaceAll(n, "_", " ")+">")
		}
		parts = append(parts, fmt.Sprintf("%q: %s", n, v))
	}
	return "{" + strings.Join(parts, ", ") + "}"
}

// installRun writes the skills for an agent: kind "claude" into dir (default ~/.claude/skills), kind "agents" into file (printed without one).
func installRun(kind, dir, file string, stdout, stderr io.Writer) int {
	all, err := skills.All()
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return usage
	}
	switch kind {
	case "claude":
		target := dir
		if target == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				fmt.Fprintf(stderr, "error: %v\n", err)
				return usage
			}
			target = filepath.Join(home, ".claude", "skills")
		}
		written, err := installClaude(target, all)
		if err != nil {
			fmt.Fprintf(stderr, "error: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "wrote %d skills under %s\n", len(written), target)
	case "agents":
		if file == "" {
			fmt.Fprint(stdout, agentsBlock(all))
			return 0
		}
		if err := installAgents(file, all); err != nil {
			fmt.Fprintf(stderr, "error: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "updated %s\n", file)
	}
	return 0
}
