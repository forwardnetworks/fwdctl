package skills

import (
	"encoding/json"
	"os"
	"testing"
)

// routingOffline scores `fwdctl which` (the offline lexical router) against evals/routing.json: how often the expected skill (or the
// playbook that governs it) is first, and how often it is anywhere in the top four. It is a floor for the router table, not the model eval.
func routingOffline(t *testing.T) (n, top1, top4 int, misses []string) {
	t.Helper()
	b, err := os.ReadFile("../evals/routing.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Query  string `json:"query"`
		Expect string `json:"expect"`
	}
	if err := json.Unmarshal(b, &cases); err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		if c.Expect == "" || c.Expect == "none" {
			continue
		}
		n++
		got, _ := Route(c.Query, 4)
		target, _ := Resolve(c.Expect)
		hit := -1
		for i, s := range got {
			if s.Skill == target {
				hit = i
				break
			}
		}
		if hit == 0 {
			top1++
		}
		if hit >= 0 {
			top4++
		} else {
			misses = append(misses, c.Expect+": "+c.Query)
		}
	}
	return
}

func TestRoutingOfflineReport(t *testing.T) {
	n, top1, top4, misses := routingOffline(t)
	t.Logf("offline routing: %d cases, top1 %d, top4 %d", n, top1, top4)
	for _, m := range misses {
		t.Logf("MISS %s", m)
	}
}
