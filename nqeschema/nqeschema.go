// Package nqeschema is Forward's NQE data model. The public source tree carries only this stub: the data model is Forward's and is not part of the public
// API client; the official release binaries include it.
package nqeschema

import "errors"

// ErrNoModel means this build carries no data model.
var ErrNoModel = errors.New("nqeschema: this build has no NQE data model (use an official fwdctl release binary)")

// Field is one node of the data model.
type Field struct {
	Path     string `json:"path"`
	Name     string `json:"name"`
	Type     string `json:"type"`
	Doc      string `json:"doc,omitempty"`
	Nullable bool   `json:"nullable,omitempty"`
	Enum     string `json:"enum,omitempty"`
}

// EnumType is a closed set of values.
type EnumType struct {
	Name         string   `json:"name"`
	Doc          string   `json:"doc,omitempty"`
	Alternatives []string `json:"alternatives"`
}

// Finding is one problem the static schema check found.
type Finding struct {
	Kind       string   `json:"kind"`
	Text       string   `json:"text"`
	Message    string   `json:"message"`
	Suggestion []string `json:"suggestions,omitempty"`
}

// Model is the loaded schema (never available here).
type Model struct {
	Fields []Field
	Enums  map[string]*EnumType
}

// Load always fails in this build.
func Load() (*Model, error) { return nil, ErrNoModel }

// Available is false.
func Available() bool { return false }

// Search and Check have nothing to search.
func (*Model) Search(string, int) []Field { return nil }
func (*Model) Check(string) []Finding     { return nil }
func (*Model) Children(string) []Field    { return nil }
