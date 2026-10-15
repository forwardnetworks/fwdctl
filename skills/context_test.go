package skills_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/forwardnetworks/fwdctl/fwdtest"
	"github.com/forwardnetworks/fwdctl/nqeschema"
	"github.com/forwardnetworks/fwdctl/skills"
)

const fakeLiveSchema = `{"type":"Record","name":"Network","fields":[{"name":"devices","type":{"type":"Bag","keyFields":["name"],"itemType":{"type":"Record","name":"Device","fields":[{"name":"liveOnlyField","type":{"type":"String"},"docString":"present only in this org's live schema"}]}}}]}`

func TestContextSchemaFallsBackToStaticWithNoSession(t *testing.T) {
	out, err := skills.Context(context.Background(), "schema", "vrf", 3, nil)
	if err != nil {
		t.Fatal(err)
	}
	src, _ := out["source"].(string)
	if !strings.HasPrefix(src, "static") || !strings.Contains(src, "no Forward login") {
		t.Errorf("source = %q", src)
	}
}

func TestContextSchemaUsesLiveWhenSessionCanReadIt(t *testing.T) {
	sess, _ := fwdtest.New(t, map[string]fwdtest.Handler{
		"GET /api/nqe/schema": func(*http.Request, []byte) (int, any) {
			return 200, []byte(fakeLiveSchema)
		},
	})
	out, err := skills.Context(context.Background(), "schema", "liveOnlyField", 3, sess)
	if err != nil {
		t.Fatal(err)
	}
	src, _ := out["source"].(string)
	if !strings.HasPrefix(src, "live") {
		t.Errorf("source = %q", src)
	}
	fields, _ := out["fields"].([]nqeschema.Field)
	if len(fields) == 0 || fields[0].Name != "liveOnlyField" {
		t.Errorf("fields = %+v", fields)
	}
}

func TestContextSchemaFallsBackWhenTheLiveReadFails(t *testing.T) {
	sess, _ := fwdtest.New(t, map[string]fwdtest.Handler{
		"GET /api/nqe/schema": func(*http.Request, []byte) (int, any) {
			return 403, map[string]any{"message": "Missing permission: OrgOperation.VIEW_NQE_LIBRARY"}
		},
	})
	out, err := skills.Context(context.Background(), "schema", "vrf", 3, sess)
	if err != nil {
		t.Fatal(err)
	}
	src, _ := out["source"].(string)
	if !strings.HasPrefix(src, "static") || !strings.Contains(src, "could not be read") {
		t.Errorf("source = %q", src)
	}
}

// A record-typed match is expanded one level: the field names and types inside it, so an author does not learn that a scope or rulebase has no `name` from a compile error.
func TestContextSchemaExpandsARecordOneLevel(t *testing.T) {
	out, err := skills.Context(context.Background(), "schema", "securityPolicy.scopes.rulebases.rules", 3, nil)
	if err != nil {
		t.Fatal(err)
	}
	kids, _ := out["contains"].(map[string][]map[string]string)
	rules := kids["network.devices.securityPolicy.scopes.rulebases.rules"]
	if len(rules) < 3 {
		t.Fatalf("the rule record should list its fields: %v", out["contains"])
	}
	rulebases := kids["network.devices.securityPolicy.scopes.rulebases"]
	names := map[string]bool{}
	for _, f := range rulebases {
		names[f["name"]] = true
		if f["type"] == "" {
			t.Errorf("field %v has no type", f)
		}
	}
	if names["name"] {
		t.Errorf("the schema says a rulebase has no name, but the expansion lists one: %v", rulebases)
	}
	if !names["rules"] {
		t.Errorf("a rulebase holds rules: %v", rulebases)
	}
}
