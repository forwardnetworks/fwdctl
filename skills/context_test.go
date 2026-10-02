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
