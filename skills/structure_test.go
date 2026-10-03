package skills

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// How an AI is told how and when to use a skill (docs/architecture.md, following Anthropic's context-engineering guidance): descriptions, the
// router and playbooks, the result's next actions, delivery, and the evals. These tests keep the router, playbooks, write protocol and READMEs
// consistent with the registry, so a new skill cannot be added without being routed, listed in the READMEs and covered by the write protocol.

func metas(t *testing.T) map[string]Meta {
	t.Helper()
	all, err := All()
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]Meta{}
	for _, m := range all {
		out[m.Name] = m
	}
	return out
}

// Every procedure (a skill that does not run) must be reachable from the router: it is the only way an agent learns the procedure exists.
func TestEveryProcedureIsNamedByTheRouter(t *testing.T) {
	ms := metas(t)
	router := RouterText()
	for name, m := range ms {
		if !m.Runnable && name != "plan-investigation" && !strings.Contains(router, "`"+name+"`") {
			t.Errorf("procedure %s is not named in plan-investigation", name)
		}
	}
}

// A playbook's tools line names skills, and only real ones: a renamed skill must not leave a playbook pointing at nothing.
func TestPlaybookToolsAreRealSkills(t *testing.T) {
	ms := metas(t)
	for name, m := range ms {
		if !strings.HasPrefix(name, "plan-") {
			continue
		}
		for _, tool := range m.Tools {
			if _, ok := ms[strings.TrimSpace(tool)]; !ok {
				t.Errorf("%s names %q in tools, which is not a skill", name, tool)
			}
		}
	}
}

var verbToken = regexp.MustCompile("`((?:inspect|investigate|check|verify|compare|find|review|author|plan|edit|validate|analyze|annotate|manage|draft|start|list)-[a-z0-9-]+)`")

// Every skill name a playbook or the router writes in backticks must exist (or be an alias): a typo would send an agent to a skill that is not there.
func TestSkillNamesInProceduresExist(t *testing.T) {
	ms := metas(t)
	aliases := Aliases()
	for name, m := range ms {
		if m.Runnable {
			continue
		}
		for _, hit := range verbToken.FindAllStringSubmatch(m.Body, -1) {
			n := hit[1]
			if _, ok := ms[n]; !ok && aliases[n] == "" {
				t.Errorf("%s names `%s`, which is not a skill or an alias", name, n)
			}
		}
	}
}

// Every skill that writes is covered by the write protocol, and says in plan-investigation that it is dry-run first.
func TestEveryWriteSkillIsInTheWriteProtocol(t *testing.T) {
	ms := metas(t)
	protocol := ms["plan-safe-write"]
	for name, m := range ms {
		if m.Class != "write" {
			continue
		}
		found := false
		for _, tool := range protocol.Tools {
			found = found || strings.TrimSpace(tool) == name
		}
		if !found {
			t.Errorf("write skill %s is not in plan-safe-write's tools", name)
		}
		if !strings.Contains(RouterText(), "`"+name+"`") {
			t.Errorf("write skill %s is not routed by plan-investigation", name)
		}
	}
}

// Both READMEs list every skill, so a person (and an agent reading the README) sees the whole set.
func TestReadmesListEverySkill(t *testing.T) {
	needPrivateTree(t)
	for _, f := range []string{"../README.md", "../dist-github/README.md"} {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for name := range metas(t) {
			if !strings.Contains(string(b), "`"+name+"`") {
				t.Errorf("%s does not list %s", f, name)
			}
		}
	}
}

// Every playbook named as a skill's owner is real, every runnable skill has an owner, and a dry run points at the write protocol.
func TestPlaybookHintsCoverEverySkillAndNameRealPlaybooks(t *testing.T) {
	ms := metas(t)
	for name, m := range ms {
		if m.Runnable && playbookFor[name] == "" {
			t.Errorf("runnable skill %s has no owning playbook in playbookFor", name)
		}
	}
	for name, pb := range playbookFor {
		if _, ok := ms[name]; !ok {
			t.Errorf("playbookFor names %s, which is not a skill", name)
		}
		if p, ok := ms[pb]; !ok || p.Runnable {
			t.Errorf("playbookFor[%s] = %s is not a procedure", name, pb)
		}
		if pb == name {
			t.Errorf("%s names itself", name)
		}
	}
}
