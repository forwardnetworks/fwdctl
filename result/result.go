// Package result is the finding envelope every Forward Skill returns: evidence, not just an answer.
//
// The invariants live in Validate and the builders, in one place, so a skill cannot say "ok" or
// "failed" without evidence, and a skill that measured nothing must say "unknown" rather than pass.
// Absence is not evidence: an empty result, an unmeasured question or a zero-row check is unknown.
package result

import (
	"fmt"
	"regexp"
	"strings"
)

const SchemaID = "forward-skills/skill-result/v1"

// Status is the outcome. ok: the objective holds. failed: it does not (a finding).
// unknown: the data cannot decide. error: the skill itself could not run.
type Status string

const (
	OK      Status = "ok"
	Failed  Status = "failed"
	Unknown Status = "unknown"
	Error   Status = "error"
)

// Confidence is how the conclusion was reached.
type Confidence string

const (
	Deterministic Confidence = "deterministic" // read directly from the digital twin
	Inferred      Confidence = "inferred"      // drawn from evidence
	NoBasis       Confidence = "unknown"       // no basis
)

// Evidence kinds.
const (
	EvPath       = "path"
	EvNQE        = "nqe"
	EvConfig     = "config"
	EvState      = "state"
	EvCollection = "collection"
	EvPredict    = "predict"
	EvTopology   = "topology"
	EvPolicy     = "policy"
)

var evidenceKinds = map[string]bool{EvPath: true, EvNQE: true, EvConfig: true, EvState: true,
	EvCollection: true, EvPredict: true, EvTopology: true, EvPolicy: true}

var skillName = regexp.MustCompile(`^[a-z][a-z0-9-]{1,62}$`)

// Source is the provenance of one evidence item.
type Source struct {
	Operation  string  `json:"operation"`
	SnapshotID *string `json:"snapshot_id"`
}

// Evidence is one cited fact.
type Evidence struct {
	Type    string         `json:"type"`
	Source  Source         `json:"source"`
	Detail  map[string]any `json:"detail"`
	Summary string         `json:"summary,omitempty"`
}

// Context says what the result was read from.
type Context struct {
	// NetworkID names the network the result is about. It may be empty ONLY for an account-level result (Scope "account"),
	// such as the list of networks itself.
	NetworkID    string  `json:"network_id"`
	Scope        string  `json:"scope,omitempty"` // "" (a network) | "account"
	SnapshotID   *string `json:"snapshot_id"`
	SnapshotTime *string `json:"snapshot_time"`
	State        string  `json:"state,omitempty"` // current | historical | predicted
}

// Operation is one Forward call the skill made, for audit and API-gap analysis.
type Operation struct {
	Operation  string  `json:"operation"`
	Status     int     `json:"status"`
	Bytes      int     `json:"bytes,omitempty"`
	Rows       *int    `json:"rows"`
	Truncated  bool    `json:"truncated"`
	DurationMS float64 `json:"duration_ms,omitempty"`
}

// Result is the envelope.
type Result struct {
	Schema      string      `json:"schema"`
	Skill       string      `json:"skill"`
	Status      Status      `json:"status"`
	Finding     string      `json:"finding"`
	Confidence  Confidence  `json:"confidence"`
	Evidence    []Evidence  `json:"evidence"`
	NextActions []string    `json:"next_actions"`
	Limits      []string    `json:"limits"`
	Context     Context     `json:"context"`
	Operations  []Operation `json:"operations,omitempty"`
	// Mode and Changes are set by a skill that writes. A read leaves both empty. Mode is "dry_run" (nothing was changed:
	// Changes is the plan) or "applied" (Changes is what was done, each with how to undo it).
	Mode    string   `json:"mode,omitempty"`
	Changes []Change `json:"changes,omitempty"`
	// Omitted lists every list in this result that was cut (a cap or a page), with the total, what is shown and how to see the rest. Empty means nothing was cut. Build also writes
	// each omission into Limits, so a reader of the text alone is told too.
	Omitted []Omission `json:"omitted,omitempty"`
}

// Modes of a skill that writes.
const (
	ModeDryRun  = "dry_run"
	ModeApplied = "applied"
)

// Change is one change a writing skill plans or made. It is the record a person or a harness reads to know exactly what
// happened to Forward, and how to put it back.
type Change struct {
	Action string `json:"action"`           // for example set_note, create_check
	Target string `json:"target"`           // what it touches, for example "snapshot 1175663"
	Before any    `json:"before,omitempty"` // the value it replaces, read first, so it can be restored
	After  any    `json:"after,omitempty"`
	// Applied is false in a dry run and true once the change was made.
	Applied bool `json:"applied"`
	// Reversible says the change can be undone through Forward's API; Undo says how. An irreversible change says so, and a
	// skill that makes one must be dry-run by default and name it in its finding.
	Reversible bool   `json:"reversible"`
	Undo       string `json:"undo,omitempty"`
}

// Validate returns every violated invariant; empty means valid.
func (r Result) Validate() []string {
	var errs []string
	add := func(f string, a ...any) { errs = append(errs, fmt.Sprintf(f, a...)) }
	if r.Schema != SchemaID {
		add("schema: expected %q", SchemaID)
	}
	if !skillName.MatchString(r.Skill) {
		add("skill: %q is not a kebab-case name", r.Skill)
	}
	switch r.Status {
	case OK, Failed, Unknown, Error:
	default:
		add("status: %q is not one of ok, failed, unknown, error", r.Status)
	}
	if strings.TrimSpace(r.Finding) == "" {
		add("finding: must not be empty")
	}
	switch r.Confidence {
	case Deterministic, Inferred, NoBasis:
	default:
		add("confidence: %q is not one of deterministic, inferred, unknown", r.Confidence)
	}
	if r.Evidence == nil || r.NextActions == nil || r.Limits == nil {
		add("evidence, next_actions and limits must be arrays, not null")
	}
	if strings.TrimSpace(r.Context.NetworkID) == "" && r.Context.Scope != "account" {
		add("context.network_id: required (only an account-level result, scope \"account\", has none)")
	}
	if r.Context.Scope != "" && r.Context.Scope != "account" {
		add("context.scope: %q is not \"account\" or empty", r.Context.Scope)
	}
	switch r.Mode {
	case "", ModeDryRun, ModeApplied:
	default:
		add("mode: %q is not empty, dry_run or applied", r.Mode)
	}
	if len(r.Changes) > 0 && r.Mode == "" {
		add("changes: a result with changes must say its mode (dry_run or applied)")
	}
	for i, c := range r.Changes {
		if strings.TrimSpace(c.Action) == "" || strings.TrimSpace(c.Target) == "" {
			add("changes[%d]: action and target are required", i)
		}
		if r.Mode == ModeDryRun && c.Applied {
			add("changes[%d]: a dry run cannot report an applied change", i)
		}
		if r.Mode == ModeApplied && !c.Applied {
			add("changes[%d]: an applied result lists only changes that were made", i)
		}
		if c.Reversible && strings.TrimSpace(c.Undo) == "" {
			add("changes[%d]: a reversible change must say how to undo it", i)
		}
	}
	for i, e := range r.Evidence {
		if !evidenceKinds[e.Type] {
			add("evidence[%d].type: %q is not a known kind", i, e.Type)
		}
		if strings.TrimSpace(e.Source.Operation) == "" {
			add("evidence[%d].source.operation: required (an item without provenance is not evidence)", i)
		}
		if e.Detail == nil {
			add("evidence[%d].detail: required", i)
		}
	}
	for i, o := range r.Omitted {
		if p := o.validate(); p != "" {
			add("omitted[%d]: %s", i, p)
		}
	}
	for i, a := range r.NextActions {
		if !skillName.MatchString(a) {
			add("next_actions[%d]: %q is not a skill name", i, a)
		}
	}
	for i, l := range r.Limits {
		if strings.TrimSpace(l) == "" {
			add("limits[%d]: must not be empty", i)
		}
	}
	switch r.Status {
	case OK, Failed:
		if len(r.Evidence) == 0 {
			add("status %s needs at least one evidence item", r.Status)
		}
		if r.Confidence == NoBasis {
			add("status %s needs a confidence of deterministic or inferred", r.Status)
		}
	case Unknown:
		if r.Confidence != NoBasis {
			add("status unknown needs confidence unknown")
		}
		if len(r.Limits) == 0 {
			add("status unknown needs at least one limit saying what was not measured")
		}
	}
	return errs
}

// InvalidError is returned by the builders when an invariant is violated.
type InvalidError struct{ Problems []string }

func (e *InvalidError) Error() string { return "invalid result: " + strings.Join(e.Problems, "; ") }

// Options are the optional parts of a result.
type Options struct {
	Evidence    []Evidence
	NextActions []string
	Limits      []string
	Mode        string
	Changes     []Change
	Omitted     []Omission
}

// Build makes a validated result. It is the only sanctioned constructor.
func Build(skill string, status Status, finding string, confidence Confidence, ctx Context, o Options) (Result, error) {
	limits := nonNilStr(o.Limits)
	for _, om := range o.Omitted {
		limits = append(limits, om.Text())
	}
	r := Result{
		Schema: SchemaID, Skill: skill, Status: status, Finding: finding, Confidence: confidence, Context: ctx,
		Evidence: nonNil(o.Evidence), NextActions: nonNilStr(o.NextActions), Limits: limits,
		Mode: o.Mode, Changes: o.Changes, Omitted: o.Omitted,
	}
	if p := r.Validate(); len(p) > 0 {
		return Result{}, &InvalidError{Problems: p}
	}
	return r, nil
}

// MustBuild is Build for constructions that are correct by inspection. It panics on a programming error.
func MustBuild(skill string, status Status, finding string, confidence Confidence, ctx Context, o Options) Result {
	r, err := Build(skill, status, finding, confidence, ctx, o)
	if err != nil {
		panic(err)
	}
	return r
}

// NewUnknown is the honest answer when the data cannot decide the question. limits must say what was
// not measured.
func NewUnknown(skill, finding string, ctx Context, limits []string, o Options) (Result, error) {
	o.Limits = limits
	return Build(skill, Unknown, finding, NoBasis, ctx, o)
}

// NewError reports that the skill could not run (bad input, not a finding about the network).
func NewError(skill, message string, ctx Context) Result {
	return MustBuild(skill, Error, message, NoBasis, ctx, Options{})
}

// NewEvidence builds one evidence item. detail is never nil.
func NewEvidence(kind, operation string, snapshotID *string, detail map[string]any, summary string) Evidence {
	if detail == nil {
		detail = map[string]any{}
	}
	return Evidence{Type: kind, Source: Source{Operation: operation, SnapshotID: snapshotID}, Detail: detail, Summary: summary}
}

func nonNil(e []Evidence) []Evidence {
	if e == nil {
		return []Evidence{}
	}
	return e
}

func nonNilStr(s []string) []string {
	out := make([]string, 0, len(s))
	seen := map[string]bool{}
	for _, v := range s {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}
