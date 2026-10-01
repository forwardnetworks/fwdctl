package skills

import (
	"fmt"

	"github.com/forwardnetworks/fwdctl/knowledge"
	"github.com/forwardnetworks/fwdctl/nqeschema"
)

// Context is the offline reference a harness reads while writing an NQE query: worked examples for a question
// ("nqe"), or real data-model field and enum names for a term ("schema"). It needs no Forward connection.
func Context(topic, term string, k int) (map[string]any, error) {
	if k <= 0 {
		k = 3
	}
	switch topic {
	case "schema":
		m, err := nqeschema.Load()
		if err != nil {
			return nil, err
		}
		fields := m.Search(term, k)
		if fields == nil {
			fields = []nqeschema.Field{}
		}
		out := map[string]any{"topic": "schema", "term": term, "fields": fields}
		var enums []map[string]any
		shown := map[string]bool{}
		for _, f := range fields {
			if e := m.Enums[f.Enum]; f.Enum != "" && e != nil && !shown[e.Name] {
				shown[e.Name] = true
				enums = append(enums, map[string]any{"name": e.Name, "values": e.Alternatives})
			}
		}
		if len(enums) > 0 {
			out["enums"] = enums
		}
		return out, nil
	case "nqe":
		ex, err := knowledge.Search(term, k)
		if err != nil {
			return nil, err
		}
		if ex == nil {
			ex = []knowledge.Example{}
		}
		return map[string]any{"topic": "nqe", "question": term, "examples": ex,
			"procedure": "load the author-nqe-query skill for the language cheat sheet and the strict rules"}, nil
	}
	return nil, fmt.Errorf("unknown context topic %q (want nqe or schema)", topic)
}
