package skills_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The forward-mcp plugin is guidance for people whose agent talks to Forward's official MCP server; it has no runner. These tests keep it honest: the skill names every tool and every argument of a live tools/list
// capture (tool and argument names only), says nothing about fwdctl, leaks no internal class name or build hash, and is registered in both marketplaces with the release version.
const mcpDir = "../plugins/forward-mcp"

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestForwardMCPSkillDocumentsEveryToolAndArgument(t *testing.T) {
	var cap struct {
		Tools map[string]struct {
			Properties []string `json:"properties"`
			Required   []string `json:"required"`
		} `json:"tools"`
	}
	if err := json.Unmarshal([]byte(readFile(t, filepath.Join(mcpDir, "testdata/tools-list.json"))), &cap); err != nil {
		t.Fatal(err)
	}
	skill := readFile(t, filepath.Join(mcpDir, "skills/forward-mcp/SKILL.md"))
	if len(cap.Tools) < 10 {
		t.Fatalf("the capture lists %d tools", len(cap.Tools))
	}
	for tool, def := range cap.Tools {
		if !strings.Contains(skill, "`"+tool+"`") {
			t.Errorf("the skill does not document the tool %s", tool)
		}
		for _, arg := range def.Properties {
			if !strings.Contains(skill, "`"+arg+"`") {
				t.Errorf("the skill does not document the argument %s of %s", arg, tool)
			}
		}
	}
}

func TestForwardMCPPluginIsPublicSafeAndMCPOnly(t *testing.T) {
	var all string
	for _, p := range []string{"skills/forward-mcp/SKILL.md", "skills/forward-mcp/reference/connecting.md", ".claude-plugin/plugin.json", ".codex-plugin/plugin.json"} {
		all += readFile(t, filepath.Join(mcpDir, p)) + "\n"
	}
	for _, bad := range []string{"fwdctl", "pyats", "openclaw", "netclaw", "horizon", "skyforge", "com.forwardnetworks", ".java", "FORWARD_AUTH_BASE64"} {
		if strings.Contains(strings.ToLower(all), strings.ToLower(bad)) {
			t.Errorf("the MCP plugin must not mention %q", bad)
		}
	}
	if regexp.MustCompile(`\b[0-9a-f]{12}\b|1\.0\.0-\d{6}`).MatchString(all) {
		t.Errorf("the MCP plugin must not carry a build hash or build number")
	}
	skill := readFile(t, filepath.Join(mcpDir, "skills/forward-mcp/SKILL.md"))
	m := regexp.MustCompile(`(?m)^description: "(.*)"$`).FindStringSubmatch(skill)
	if m == nil || len(m[1]) > maxDescriptionForMCP {
		t.Errorf("the skill needs a description of at most %d characters, got %v", maxDescriptionForMCP, m)
	}
	if !strings.Contains(strings.ToLower(skill), "unofficial") {
		t.Errorf("the skill says it is unofficial guidance")
	}
}

const maxDescriptionForMCP = 190

func TestForwardMCPPluginIsRegisteredWithTheReleaseVersion(t *testing.T) {
	var main struct {
		Version string `json:"version"`
	}
	_ = json.Unmarshal([]byte(readFile(t, "../.claude-plugin/plugin.json")), &main)
	for _, p := range []string{".claude-plugin/plugin.json", ".codex-plugin/plugin.json"} {
		var m struct{ Name, Version string }
		if err := json.Unmarshal([]byte(readFile(t, filepath.Join(mcpDir, p))), &m); err != nil || m.Name != "forward-mcp" || m.Version != main.Version {
			t.Errorf("%s: name %q version %q, want forward-mcp %s (%v)", p, m.Name, m.Version, main.Version, err)
		}
	}
	for _, mp := range []string{"../.claude-plugin/marketplace.json", "../.agents/plugins/marketplace.json"} {
		if !strings.Contains(readFile(t, mp), `"forward-mcp"`) {
			t.Errorf("%s does not list forward-mcp", mp)
		}
	}
}
