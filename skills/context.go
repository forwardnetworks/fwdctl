package skills

import (
	"context"
	"fmt"

	"github.com/forwardnetworks/fwdctl/fwd"
	"github.com/forwardnetworks/fwdctl/knowledge"
	"github.com/forwardnetworks/fwdctl/nqeschema"
)

// Context is the offline reference a harness reads while writing an NQE query: worked examples for a question
// ("nqe"), or real data-model field and enum names for a term ("schema"). It needs no Forward connection.
//
// For "schema", sess is optional: nil (or any session that cannot read the schema, such as one lacking VIEW_NQE_LIBRARY) searches the
// static, sealed data model (every field Forward's product can ever have), and the result says so. A session that can read it searches the
// organization's own live, filtered model instead (feature settings, license tier, and this org's data files under network.extensions),
// and the result says that too, so a caller never confuses "this field does not exist" with "this org cannot use that field".
func Context(ctx context.Context, topic, term string, k int, sess *fwd.Session) (map[string]any, error) {
	if k <= 0 {
		k = 3
	}
	switch topic {
	case "schema":
		m, source, sourceErr := schemaModel(ctx, sess)
		if m == nil {
			return nil, sourceErr
		}
		fields := m.Search(term, k)
		if fields == nil {
			fields = []nqeschema.Field{}
		}
		out := map[string]any{"topic": "schema", "term": term, "fields": fields, "source": source}
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

// schemaModel picks the live, org-filtered model when sess can read it, the static sealed one otherwise. source describes which was used
// and why; sourceErr is returned only when neither is available (no sess, and the static corpus is not in this build).
func schemaModel(ctx context.Context, sess *fwd.Session) (m *nqeschema.Model, source string, sourceErr error) {
	liveFailure := ""
	if sess != nil {
		raw, err := sess.NQESchema(ctx)
		if err == nil {
			if live, perr := nqeschema.ParseModel(raw); perr == nil {
				return live, "live: this organization's schema (feature settings, license tier, and its data files)", nil
			} else {
				liveFailure = perr.Error()
			}
		} else if op, ok := fwd.MissingPermission(err); ok {
			liveFailure = "this login lacks " + op
		} else {
			liveFailure = err.Error()
		}
	}
	static, err := nqeschema.Load()
	if err != nil {
		return nil, "", err
	}
	note := "static: the full product schema, not filtered to this organization's feature settings or license tier"
	switch {
	case sess == nil:
		note += " (no Forward login)"
	case liveFailure != "":
		note += " (the live schema could not be read: " + liveFailure + ")"
	}
	return static, note, nil
}
