// Package skills holds the Forward Skills: procedures that accomplish a network-engineering objective
// with Forward and return evidence (see package result).
//
// Each skill is a directory holding SKILL.md (the procedure, in the frontmatter shape Claude Code and
// an embedding platform's skill loaders use) and schema.json (its input), plus Go code registered here. A skill with
// no Go code is a procedure-only skill: the harness loads its body and follows it.
package skills

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"github.com/forwardnetworks/fwdctl/knowledge"
	"sort"
	"strconv"
	"strings"

	"github.com/forwardnetworks/fwdctl/fwd"
	"github.com/forwardnetworks/fwdctl/jsonschema"
	"github.com/forwardnetworks/fwdctl/result"
)

//go:embed */SKILL.md */schema.json */reference/*.md
var docs embed.FS

// Meta describes a skill to a harness.
type Meta struct {
	Name          string          `json:"name"`
	Description   string          `json:"description"`
	Maturity      int             `json:"maturity"`
	Compatibility string          `json:"compatibility,omitempty"`
	Tools         []string        `json:"tools,omitempty"`
	Body          string          `json:"-"`
	InputSchema   json.RawMessage `json:"input_schema,omitempty"`
	// References are files beside SKILL.md (reference/*.md) that a harness reads only when it needs them.
	References []string `json:"references,omitempty"`
	Runnable   bool     `json:"runnable"`
	// Class is "read" (the default) or "write". A write skill is dry-run unless its input says apply:true, and its result lists
	// every Change with how to undo it. Reversible is false when any change it can make cannot be undone through the API.
	Class      string `json:"class"`
	Reversible bool   `json:"reversible,omitempty"`
	// Effect is how far a write reaches: "network", "snapshot" or "org" (empty for a read). Secrets is true when the skill takes,
	// references or can copy a secret (a password, a credential id, a header value, uploaded content); SecretsSet is whether the
	// skill declared it, which a write skill must.
	Effect     string `json:"effect,omitempty"`
	Secrets    bool   `json:"secrets"`
	SecretsSet bool   `json:"-"`
	// Cluster is the area of the skill set a skill belongs to (see Clusters), and Summary a phrase of at most six words that
	// stands for it in the compact list `fwdctl install agents` writes. Both come from metadata in SKILL.md; every skill must carry them.
	Cluster string `json:"cluster,omitempty"`
	Summary string `json:"summary,omitempty"`
}

// Runner is a skill's Go implementation. in is already validated against the skill's schema.
type Runner func(ctx context.Context, s *fwd.Session, in json.RawMessage) (result.Result, error)

var runners = map[string]Runner{}

// Register adds a skill implementation. It panics on a duplicate or a skill with no SKILL.md, so a
// mismatch between code and docs fails at start, not at first use.
func Register(name string, r Runner) {
	if _, dup := runners[name]; dup {
		panic("skills: duplicate registration of " + name)
	}
	if _, err := docs.ReadFile(name + "/SKILL.md"); err != nil {
		panic("skills: " + name + " has no SKILL.md")
	}
	runners[name] = r
}

// An alias keeps a retired skill name working: it runs the surviving skill with the retired skill's fixed inputs. Merged and
// renamed skills are listed here, so a design or harness that names the old skill is not broken by the unification.
//
// Rename maps an input name the retired skill took to the one the surviving skill takes (applied before Preset), so a caller that still
// sends the old inputs works unchanged.
type alias struct {
	To     string
	Preset map[string]any
	Rename map[string]string
}

var aliases = map[string]alias{
	"annotate-snapshot":             {"edit-snapshot-note", nil, nil},
	"manage-checks":                 {"edit-checks", nil, nil},
	"draft-change-set":              {"edit-change-set", nil, nil},
	"start-collection":              {"edit-collection", nil, nil},
	"investigate-vulnerabilities":   {"inspect-vulnerabilities", nil, nil},
	"list-networks":                 {"inspect-networks", nil, nil},
	"inspect-collection-status":     {"inspect-collection", map[string]any{"view": "status"}, nil},
	"inspect-collection-config":     {"inspect-collection", map[string]any{"view": "config"}, nil},
	"inspect-external-connectivity": {"inspect-topology", map[string]any{"kind": "external"}, nil},
	"analyze-blast-radius":          {"verify-change", map[string]any{"view": "impact"}, nil},
	"inspect-checks":                {"check-network-compliance", map[string]any{"view": "read"}, nil},
	"review-change-set":             {"verify-change", map[string]any{"view": "describe"}, nil},
	"plan-author-query":             {"author-nqe-query", nil, nil},     // a procedure folded into the authoring guide; it has no runner
	"plan-health-check":             {"plan-incident-triage", nil, nil}, // folded in as its health check section
	"find-ip-owner":                 {"inspect-inventory", map[string]any{"kind": "ip_owner"}, nil},
	"find-public-addresses":         {"inspect-edge", map[string]any{"view": "public_addresses"}, nil},
	"find-trace-source":             {"inspect-edge", map[string]any{"view": "trace_sources"}, nil},
	"compare-link-overrides":        {"inspect-topology", map[string]any{"kind": "link_overrides"}, map[string]string{"before_snapshot_id": "compare_to_snapshot_id", "after_snapshot_id": "snapshot_id"}},
}

// Cluster is one area of the skill set. Every skill declares one in its metadata, so a new skill has to say which existing skills it
// sits beside (and why it is not a view of one of them): see "Adding a skill" in docs/internal.md.
type Cluster struct {
	Name  string
	Title string
}

// Clusters lists the areas, in the order the compact skill list prints them.
var Clusters = []Cluster{
	{"environment", "Start here and protocol"},
	{"access", "Access and org settings"},
	{"investigate", "Connectivity and triage"},
	{"snapshots", "Snapshots and collection"},
	{"inventory-topology", "Inventory, topology, device state"},
	{"change", "Change and comparison"},
	{"compliance", "Compliance and history"},
	{"security", "Security and vulnerabilities"},
	{"nqe", "NQE queries"},
	{"edge-synthetic", "Edge, synthetic devices, overrides"},
}

// Resolve returns the skill an old or current name stands for and the inputs it fixes.
func Resolve(name string) (string, map[string]any) {
	if a, ok := aliases[name]; ok {
		return a.To, a.Preset
	}
	return name, nil
}

// AliasNote says, for a retired name, what it now stands for ("inspect-edge with view=trace_sources"), or "" when name is not an alias.
func AliasNote(name string) string {
	a, ok := aliases[name]
	if !ok {
		return ""
	}
	var fixed []string
	for k, v := range a.Preset {
		fixed = append(fixed, fmt.Sprintf("%s=%v", k, v))
	}
	sort.Strings(fixed)
	note := a.To
	if len(fixed) > 0 {
		note += " with " + strings.Join(fixed, ", ")
	}
	var renamed []string
	for from, to := range a.Rename {
		renamed = append(renamed, from+" is "+to)
	}
	sort.Strings(renamed)
	if len(renamed) > 0 {
		note += " (input " + strings.Join(renamed, ", ") + ")"
	}
	return note
}

// aliasRename is the input renames of an alias (nil when none).
func aliasRename(name string) map[string]string { return aliases[name].Rename }

// Aliases lists the retired names that still run, with what each maps to.
func Aliases() map[string]string {
	out := make(map[string]string, len(aliases))
	for k, v := range aliases {
		out[k] = v.To
	}
	return out
}

// ErrUnknown means no such skill; ErrInvalidInput means the input failed its schema.
var (
	ErrUnknown      = fmt.Errorf("no such skill")
	ErrInvalidInput = fmt.Errorf("invalid input")
)

// Names lists every skill that has a SKILL.md, sorted.
func Names() []string {
	entries, _ := docs.ReadDir(".")
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			if _, err := docs.ReadFile(e.Name() + "/SKILL.md"); err == nil {
				names = append(names, e.Name())
			}
		}
	}
	sort.Strings(names)
	return names
}

// Describe returns a skill's metadata.
func Describe(name string) (Meta, error) {
	name, _ = Resolve(name)
	src, err := docs.ReadFile(name + "/SKILL.md")
	if err != nil {
		return Meta{}, fmt.Errorf("%w: %s", ErrUnknown, name)
	}
	m, err := parseSkill(name, string(src))
	if err != nil {
		return Meta{}, err
	}
	if sch, err := docs.ReadFile(name + "/schema.json"); err == nil {
		m.InputSchema = sch
	}
	m.References = referencesOf(name)
	_, m.Runnable = runners[name]
	return m, nil
}

// All describes every skill.
func All() ([]Meta, error) {
	var out []Meta
	for _, n := range Names() {
		m, err := Describe(n)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, nil
}

// Run validates the input, runs the skill, and validates the result before anyone sees it.
func Run(ctx context.Context, name string, s *fwd.Session, in json.RawMessage) (result.Result, error) {
	asked := name
	name, preset := Resolve(name)
	rename := aliasRename(asked)
	if len(preset) > 0 || len(rename) > 0 {
		var m map[string]any
		if err := json.Unmarshal(in, &m); err != nil || m == nil {
			return result.Result{}, fmt.Errorf("%w: inputs must be a JSON object", ErrInvalidInput)
		}
		for from, to := range rename {
			if v, ok := m[from]; ok {
				delete(m, from)
				if _, dup := m[to]; !dup {
					m[to] = v
				}
			}
		}
		for k, v := range preset {
			m[k] = v
		}
		in, _ = json.Marshal(m)
	}
	m, err := Describe(name)
	if err != nil {
		return result.Result{}, err
	}
	run, ok := runners[name]
	if !ok {
		return result.Result{}, fmt.Errorf("%w: %s is a procedure-only skill; load its body instead", ErrUnknown, name)
	}
	if len(m.InputSchema) > 0 {
		problems, err := jsonschema.Validate(m.InputSchema, in)
		if err != nil {
			return result.Result{}, err
		}
		if len(problems) > 0 {
			return result.Result{}, fmt.Errorf("%w: %s", ErrInvalidInput, strings.Join(problems, "; "))
		}
	}
	r, err := run(ctx, s, in)
	if err != nil {
		// A refusal for missing permission or licence reads the same for every skill: what was refused, what it needs, who can resolve it.
		dr, ok := denialResult(name, err, result.Context{Scope: "account", State: "current"}, "the request")
		if !ok {
			return result.Result{}, err
		}
		r = dr
	}
	if len(r.Operations) == 0 {
		r.Operations = s.Operations()
	}
	if s.Insecure() {
		r.Limits = append(r.Limits, "TLS certificate verification was disabled for this run; the connection to Forward could have been intercepted")
	}
	addPlaybookHints(name, &r)
	if p := r.Validate(); len(p) > 0 {
		return result.Result{}, &result.InvalidError{Problems: p}
	}
	return r, nil
}

// parseSkill reads `---` frontmatter and the body. The parser is hand-rolled on purpose (descriptions are
// prose with colons and quotes, which a strict YAML load rejects); it splits on the FIRST colon of a line.
func parseSkill(name, src string) (Meta, error) {
	m := Meta{Name: name}
	src = strings.ReplaceAll(src, "\r\n", "\n")
	if !strings.HasPrefix(src, "---\n") {
		return m, fmt.Errorf("skill %q: missing opening --- frontmatter fence", name)
	}
	rest := src[4:]
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return m, fmt.Errorf("skill %q: unterminated frontmatter", name)
	}
	inMetadata := false
	for _, raw := range strings.Split(rest[:end], "\n") {
		line := strings.TrimSpace(raw)
		i := strings.Index(line, ":")
		if line == "" || strings.HasPrefix(line, "#") || i < 0 {
			continue
		}
		k, v := strings.TrimSpace(line[:i]), strings.Trim(strings.TrimSpace(line[i+1:]), `"`)
		if strings.HasPrefix(raw, " ") || strings.HasPrefix(raw, "\t") {
			if inMetadata { // a key nested under metadata:
				switch k {
				case "maturity":
					m.Maturity, _ = strconv.Atoi(v)
				case "class":
					m.Class = v
				case "cluster":
					m.Cluster = v
				case "summary":
					m.Summary = v
				case "reversible":
					m.Reversible = v == "true"
				case "effect":
					m.Effect = v
				case "secrets":
					m.Secrets, m.SecretsSet = v == "true", true
				case "tools":
					for _, t := range strings.Split(v, ",") {
						if t = strings.TrimSpace(t); t != "" {
							m.Tools = append(m.Tools, t)
						}
					}
				}
			}
			continue
		}
		inMetadata = k == "metadata"
		switch k {
		case "name":
			if v != name {
				return m, fmt.Errorf("skill %q: frontmatter name %q does not match its directory", name, v)
			}
		case "description":
			m.Description = v
		case "compatibility":
			m.Compatibility = v
		}
	}
	m.Body = strings.TrimPrefix(rest[end+4:], "\n")
	if m.Class == "" {
		m.Class = "read"
	}
	if m.Class != "read" && m.Class != "write" {
		return m, fmt.Errorf("skill %q: metadata class %q must be read or write", name, m.Class)
	}
	if m.Description == "" {
		return m, fmt.Errorf("skill %q: no description", name)
	}
	return m, nil
}

// referencesOf lists a skill's reference files, as paths relative to the skill folder ("reference/rules.md"). For
// author-nqe-query this also lists the authoring references the binary seals (the language guides, the std-lib digest,
// the cheat sheet): they are not files beside the skill (docs/internal.md, "the release build seals them"), but they are
// real reference files all the same, and a caller listing this skill's references should see them.
func referencesOf(name string) []string {
	entries, err := docs.ReadDir(name + "/reference")
	var out []string
	if err == nil {
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
				out = append(out, "reference/"+e.Name())
			}
		}
	}
	if name == "author-nqe-query" {
		for _, n := range knowledge.AuthoringRefs() {
			out = append(out, "reference/"+n)
		}
	}
	sort.Strings(out)
	return out
}

// Reference returns one reference file of a skill. ref is the relative path ("reference/rules.md") or just the file name.
func Reference(name, ref string) (string, error) {
	if !strings.Contains(ref, "/") {
		ref = "reference/" + ref
	}
	if name == "author-nqe-query" { // the authoring references are served by the binary, not shipped beside the skill: try them before the embedded files
		if t, ok := knowledge.AuthoringRef(ref); ok {
			return t, nil
		}
	}
	for _, r := range referencesOf(name) {
		if r == ref || r == ref+".md" {
			b, err := docs.ReadFile(name + "/" + r)
			return string(b), err
		}
	}
	return "", fmt.Errorf("%w: %s has no reference %q", ErrUnknown, name, ref)
}
