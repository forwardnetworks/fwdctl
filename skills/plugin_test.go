package skills_test

import (
	"encoding/json"
	"os"
	"testing"
)

// The plugin is installed as forward-skills@forward-skills. A blanket rename of the binary once turned both names into
// "fwdctl" and the install failed with "plugin not found"; this keeps the manifests honest.
func TestPluginManifestsNameTheMarketplaceAndPluginForwardSkills(t *testing.T) {
	needPrivateTree(t)
	var mk struct {
		Name    string
		Plugins []struct{ Name, Source string }
	}
	var pl struct{ Name string }
	for path, dst := range map[string]any{"../.claude-plugin/marketplace.json": &mk, "../.claude-plugin/plugin.json": &pl} {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(b, dst); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
	}
	if mk.Name != "forward-skills" || pl.Name != "forward-skills" || len(mk.Plugins) != 1 || mk.Plugins[0].Name != "forward-skills" || mk.Plugins[0].Source != "." {
		t.Fatalf("marketplace %+v plugin %+v", mk, pl)
	}
}
