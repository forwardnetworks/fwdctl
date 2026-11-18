package skills_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/forwardnetworks/fwdctl/skills"
)

// The SOC toolkit map (plan-security-posture/reference/soc-toolkit-map.md) says which skill answers each SOC question and where the gaps are. A name that is not a registered skill sends an agent to
// nothing, a kind, view, input or reference file that does not exist is the same mistake one level down, and a map that stops saying what is missing stops being honest: every table row has five
// cells and a status; covered and partial rows name a skill; gap and outside rows name none; the map keeps rows of each status.
func TestSocToolkitMapNamesOnlyRealThingsAndKeepsItsGaps(t *testing.T) {
	b, err := os.ReadFile("plan-security-posture/reference/soc-toolkit-map.md")
	if err != nil {
		t.Fatal(err)
	}
	text := string(b)
	known := map[string]bool{}
	for _, n := range skills.Names() {
		known[n] = true
	}
	prefix := regexp.MustCompile(`^(inspect|investigate|check|plan|verify|compare|edit|find|author|validate)-[a-z-]+$`)
	token := regexp.MustCompile("`([^`]+)`")
	statuses := map[string]int{}
	rows := 0
	for _, line := range strings.Split(text, "\n") {
		if !strings.HasPrefix(line, "| ") || strings.HasPrefix(line, "| Workflow |") {
			continue
		}
		cells := strings.Split(strings.Trim(line, "| "), " | ")
		if len(cells) != 5 {
			t.Errorf("%d cells, want 5: %q", len(cells), line)
			continue
		}
		rows++
		status := cells[3]
		if status != "covered" && status != "partial" && status != "gap" && status != "outside" {
			t.Errorf("status %q in %q", status, cells[1])
		}
		statuses[status]++
		cited := map[string]bool{}
		var others []string
		for _, tk := range token.FindAllStringSubmatch(cells[2], -1) {
			if prefix.MatchString(tk[1]) {
				cited[tk[1]] = true
				if !known[tk[1]] {
					t.Errorf("%q names %q, which is not a registered skill", cells[1], tk[1])
				}
			} else {
				others = append(others, tk[1])
			}
		}
		if (status == "covered" || status == "partial") && len(cited) == 0 && !strings.Contains(cells[2], "`fwdctl ") {
			t.Errorf("%q is %s but names no skill", cells[1], status)
		}
		if (status == "gap" || status == "outside") && (len(cited) != 0 || cells[2] != "(none)") {
			t.Errorf("%q is %s and must name no skill: %q", cells[1], status, cells[2])
		}
		allowed := map[string]bool{}
		for sk := range cited {
			for v := range schemaNames(t, sk) {
				allowed[v] = true
			}
		}
		for _, o := range others {
			switch {
			case strings.HasSuffix(o, ".md"):
				found := false
				for sk := range cited {
					if _, err := os.Stat(filepath.Join(sk, "reference", o)); err == nil {
						found = true
					}
				}
				if !found {
					t.Errorf("%q names the reference %q, which is not in a cited skill's reference folder", cells[1], o)
				}
			case allowed[o], socMapNonSkillNames[o]:
			default:
				t.Errorf("%q names %q: not a kind, view or input of %v, and not a listed non-skill name", cells[1], o, keys(cited))
			}
		}
	}
	// rows a review found wrongly marked once: Forward's audit log is read (inspect-access view activity), Forward's own accounts are read and managed, and internet exposure is only partial
	pinned := map[string]string{"Who changed what in Forward itself": "covered", "Who has access to Forward": "covered", "What is exposed to the internet": "partial"}
	for _, line := range strings.Split(text, "\n") {
		for q, want := range pinned {
			if strings.Contains(line, "| "+q) {
				if cells := strings.Split(strings.Trim(line, "| "), " | "); len(cells) == 5 {
					if cells[3] != want {
						t.Errorf("%q is %s, expected %s", q, cells[3], want)
					}
					delete(pinned, q)
				}
			}
		}
	}
	if len(pinned) != 0 {
		t.Errorf("rows missing from the map: %v", pinned)
	}
	if rows < 38 {
		t.Errorf("only %d rows: the map was cut", rows)
	}
	if statuses["covered"] < 14 || statuses["partial"] < 12 || statuses["gap"]+statuses["outside"] < 6 {
		t.Errorf("the map should say what is covered, partial, a gap and outside: %v", statuses)
	}
}

// A gap row says no skill answers a question. That stops being true when a skill is added, and an agent would keep telling a SOC it cannot be done (the audit log was once listed as a gap while
// inspect-access view activity read it). For each gap, these words must not appear in any skill's name, description or summary; when one does, the row is stale and must change.
func TestSocToolkitMapGapsAreNotCoveredBySomeSkill(t *testing.T) {
	b, err := os.ReadFile("plan-security-posture/reference/soc-toolkit-map.md")
	if err != nil {
		t.Fatal(err)
	}
	gaps := map[string]*regexp.Regexp{
		"Incidents raised from device SNMP traps":              regexp.MustCompile(`(?i)\btraps?\b`),
		"Which device certificates are about to expire":        regexp.MustCompile(`(?i)certificate expir|cert expir|expiring certificate`),
		"Wireless networks (SSIDs) and network access control": regexp.MustCompile(`(?i)wireless|\bssid|802\.1x|\bNAC\b`),
	}
	all, err := skills.All()
	if err != nil {
		t.Fatal(err)
	}
	seen := 0
	for _, line := range strings.Split(string(b), "\n") {
		for q, re := range gaps {
			if !strings.Contains(line, "| "+q) {
				continue
			}
			seen++
			if !strings.Contains(line, "| gap |") && !strings.Contains(line, "| outside |") {
				t.Errorf("%q is expected to be a gap or outside row: %q", q, line)
			}
			for _, m := range all {
				if re.MatchString(m.Name + " " + m.Description + " " + m.Summary) {
					t.Errorf("gap %q: skill %s now mentions it (%q): update the map", q, m.Name, re.String())
				}
			}
		}
	}
	if seen != len(gaps) {
		t.Errorf("found %d of the %d expected gap rows", seen, len(gaps))
	}
}

// socMapNonSkillNames are names a SOC map row may use that are not in a skill's schema: an NQE field of the data model and the one CLI command that is not a skill.
var socMapNonSkillNames = map[string]bool{"device.services": true, "topic: security": true, "fwdctl device-file export": true}
