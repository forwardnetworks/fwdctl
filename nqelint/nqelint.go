// Package nqelint is the NQE linter, editor support and synthetic-device row checker. The public source tree carries only this stub: the NQE authoring
// tooling is not part of the public API client, and the official release binaries include the real one. Every function here reports that it is not available.
package nqelint

import (
	"errors"
	"io"
)

// ErrNotInThisBuild is what the authoring features answer in a build from the public source.
var ErrNotInThisBuild = errors.New("NQE authoring tools (lint, format, editor support) are not in this build: use an official fwdctl release binary")

// Diagnostic is one finding. Line and Column are 1-based.
type Diagnostic struct {
	Severity string `json:"severity"`
	Line     int    `json:"line"`
	Column   int    `json:"column"`
	Length   int    `json:"length,omitempty"`
	Code     string `json:"code"`
	Message  string `json:"message"`
	Fix      string `json:"fix,omitempty"`
}

// HasErrors reports whether any diagnostic is an error.
func HasErrors(d []Diagnostic) bool {
	for _, x := range d {
		if x.Severity == "error" {
			return true
		}
	}
	return false
}

// CorporaPresent is false in this build.
func CorporaPresent() bool { return false }

// Lint finds nothing in this build and says why in one warning, so a clean result is never read as a pass.
func Lint(src string) []Diagnostic {
	return []Diagnostic{{Severity: "warning", Line: 1, Column: 1, Code: "no-authoring-tools", Message: ErrNotInThisBuild.Error() + "; validate-nqe-query asks Forward"}}
}

// LintWith is Lint.
func LintWith(src string, _ ModuleSource) []Diagnostic { return Lint(src) }

// ModuleSig is an imported module's signature (not available here).
type ModuleSig struct{}

// ModuleSource resolves imported modules.
type ModuleSource interface {
	Module(path string) (*ModuleSig, error)
}

type none struct{}

func (none) Module(string) (*ModuleSig, error) { return nil, ErrNotInThisBuild }

// Modules is the default module source.
var Modules ModuleSource = none{}

// Dir and LibraryDir read modules from a directory (not available here).
func Dir(string) ModuleSource        { return none{} }
func LibraryDir(string) ModuleSource { return none{} }

// Format is not available in this build.
func Format(string) (string, error) { return "", ErrNotInThisBuild }

// ServeLSP is not available in this build.
func ServeLSP(io.Reader, io.Writer) error { return ErrNotInThisBuild }

// CompletionItem is one editor completion.
type CompletionItem struct {
	Label      string `json:"label"`
	Kind       string `json:"kind"`
	Detail     string `json:"detail,omitempty"`
	Doc        string `json:"doc,omitempty"`
	Deprecated bool   `json:"deprecated,omitempty"`
}

// CompleteAtWith offers nothing in this build.
func CompleteAtWith(string, int, int, ModuleSource) []CompletionItem { return nil }

// Hover is the text for a position.
type Hover struct {
	Span     Span
	Markdown string
}

// Span is a source range, 1-based line and column; the end column is exclusive.
type Span struct{ Line, Col, EndLine, EndCol int }

// Analysis is a parsed query (not available here).
type Analysis struct{}

// AnalyzeWith returns an empty analysis.
func AnalyzeWith(string, ModuleSource) *Analysis { return &Analysis{} }

// HoverAt has nothing to show in this build.
func (*Analysis) HoverAt(int, int) *Hover { return nil }

// CheckSyntheticRows cannot check in this build; the caller says so.
func CheckSyntheticRows(string, string) ([]Diagnostic, error) { return nil, nil }

// SyntheticKindNames lists the synthetic device kinds.
func SyntheticKindNames() []string {
	return []string{"adjacent-network", "internet", "intranet", "l2vpn", "l3vpn"}
}

// BundleSource gives a bundle the source of a library module (not available here).
type BundleSource interface {
	Source(path string) (string, error)
}

// ErrNoSuchModule is what a BundleSource returns for a path it does not hold.
var ErrNoSuchModule = errors.New("no such module")

// Bundle is not available in this build.
func Bundle(string, BundleSource) (string, []string, error) { return "", nil, ErrNotInThisBuild }
