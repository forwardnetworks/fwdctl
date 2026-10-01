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
