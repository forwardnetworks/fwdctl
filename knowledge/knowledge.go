// Package knowledge holds worked NQE examples. The public source tree carries only this stub: the examples are Forward's and are not part of the public
// API client; the official release binaries include them.
package knowledge

// Example is one worked NQE example.
type Example struct {
	Description string  `json:"description"`
	Query       string  `json:"query"`
	Score       float64 `json:"score,omitempty"`
}

// Examples returns none in this build.
func Examples() ([]Example, error) { return nil, nil }

// Search returns none in this build.
func Search(string, int) ([]Example, error) { return nil, nil }

// AuthoringRef has nothing in this build: the authoring references are in the release binaries only.
func AuthoringRef(string) (string, bool) { return "", false }

// AuthoringRefs lists none.
func AuthoringRefs() []string { return nil }

// OrgProp is one organization property's definition (not available in this build).
type OrgProp struct {
	Name, Doc, Kind, Default, Enum string
	Values, Range, Uses            []string
	Level, Admin                   string
	Risk, Reason, Consequence      string
}

// OrgPropertyInfo has nothing in this build: the property table is in the release binaries only.
func OrgPropertyInfo(string) (*OrgProp, bool) { return nil, false }

// OrgPropertyNames lists none.
func OrgPropertyNames() []string { return nil }

// RBAC is the role model (not available in this build).
type RBAC struct{ Order []string }

// RBACOp is one operation (not available in this build).
type RBACOp struct{ Name, Scope, Access, License, Doc, MinRole string }

// RBACModel has nothing in this build: the role model is in the release binaries only.
func RBACModel() *RBAC { return nil }

// Op finds nothing in this build.
func (*RBAC) Op(string, string) (*RBACOp, bool) { return nil, false }

// Rank knows no role in this build.
func (*RBAC) Rank(string) int { return -1 }

// OpsAt lists none.
func (*RBAC) OpsAt(string, string) []string { return nil }
