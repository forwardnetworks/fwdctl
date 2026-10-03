package skills

import (
	"regexp"
	"sort"
	"strings"
)

// Suggestion is one skill (or playbook) the router table of plan-investigation recommends for a question, with the row that matched.
type Suggestion struct {
	Skill    string  `json:"skill"`
	Playbook bool    `json:"playbook"`
	Matched  string  `json:"matched"`
	Score    float64 `json:"score"`
}

var routeWord = regexp.MustCompile(`[a-z0-9]+`)

var routeStop = map[string]bool{"the": true, "a": true, "an": true, "of": true, "to": true, "in": true, "on": true, "for": true, "and": true, "or": true, "is": true, "are": true,
	"it": true, "this": true, "that": true, "do": true, "does": true, "we": true, "our": true, "my": true, "me": true, "i": true, "can": true, "what": true, "which": true,
	"how": true, "with": true, "any": true, "all": true, "show": true, "give": true, "tell": true, "about": true, "there": true, "from": true, "at": true, "by": true, "be": true}

func routeWords(s string) map[string]bool {
	out := map[string]bool{}
	for _, w := range routeWord.FindAllString(strings.ToLower(s), -1) {
		if len(w) > 1 && !routeStop[w] {
			out[stem(w)] = true
		}
	}
	return out
}

// stem is a light suffix trim so "failing", "fails" and "failed" meet.
func stem(w string) string {
	for _, suf := range []string{"ing", "ed", "es", "s"} {
		if len(w) > len(suf)+3 && strings.HasSuffix(w, suf) {
			return strings.TrimSuffix(w, suf)
		}
	}
	return w
}

// routeDiagnostic are (stemmed) words of a question about why something is bad; routeAction are words that ask for a change.
var routeDiagnostic = map[string]bool{"why": true, "slow": true, "slower": true, "long": true, "fail": true, "cause": true, "wrong": true, "miss": true, "broken": true, "error": true,
	"took": true, "timeout": true, "stuck": true, "hour": true, "bottleneck": true, "healthy": true}
var routeAction = map[string]bool{"create": true, "add": true, "set": true, "change": true, "delete": true, "remove": true, "enable": true, "disable": true, "start": true, "stop": true,
	"apply": true, "upload": true, "attach": true, "save": true, "update": true, "edit": true, "tag": true, "label": true, "model": true, "stage": true, "reprocess": true, "cancel": true,
	"collect now": true, "note": true}

var routeSkill = regexp.MustCompile("`([a-z][a-z0-9-]+)`")

// Route ranks the rows of plan-investigation's tables (the playbook table and the symptom table) against a question and returns the best
// skills, offline and with no model. It is a first guess for an agent or a person that has not read the router; the router itself, read
// with `describe plan-investigation`, has the full table and the limits.
func Route(question string, limit int) ([]Suggestion, error) {
	ms, err := All()
	if err != nil {
		return nil, err
	}
	var body string
	known := map[string]Meta{}
	for _, m := range ms {
		known[m.Name] = m
		if m.Name == "plan-investigation" {
			body = RouterText()
		}
	}
	want := routeWords(question)
	if len(want) == 0 {
		return nil, nil
	}
	type row struct {
		phrase, target string
		have           map[string]bool
	}
	var rows []row
	df := map[string]int{} // in how many rows a word appears: a word in many rows ("devices") says little
	for _, line := range strings.Split(body, "\n") {
		if !strings.HasPrefix(line, "|") || strings.Contains(line, "---") {
			continue
		}
		cells := strings.Split(strings.Trim(line, "| "), "|")
		if len(cells) < 2 {
			continue
		}
		r := row{phrase: strings.TrimSpace(cells[len(cells)-2]), target: cells[len(cells)-1]}
		r.have = routeWords(r.phrase)
		for w := range r.have {
			df[w]++
		}
		rows = append(rows, r)
	}
	best := map[string]Suggestion{}
	for _, r := range rows {
		phrase, target := r.phrase, r.target
		score := 0.0
		for w := range want {
			if r.have[w] {
				score += 6 / float64(1+df[w])
			}
		}
		if score == 0 {
			continue
		}
		if first := routeSkill.FindStringSubmatch(target); first != nil { // the first skill in a row is the answer; the rest are what it hands on to
			m := first
			name := m[1]
			meta, ok := known[name]
			if !ok {
				continue
			}
			s := score
			if !meta.Runnable && strings.HasPrefix(name, "plan-") && name != "plan-investigation" {
				s += 0.5 // a playbook that matches is worth reading before a single skill
			}
			if cur, have := best[name]; !have || s > cur.Score {
				best[name] = Suggestion{Skill: name, Playbook: !meta.Runnable, Matched: strings.TrimSpace(phrase), Score: s}
			}
		}
	}
	// A question that asks why something is slow, failing or wrong, and names no action, is about reading, not changing: an edit-* skill that happens to share words
	// with it ("production", "collection") must not outrank the skill that diagnoses it.
	diagnostic, action := false, false
	for w := range want {
		diagnostic = diagnostic || routeDiagnostic[w]
		action = action || routeAction[w]
	}
	if diagnostic && !action {
		for n, s := range best {
			if strings.HasPrefix(n, "edit-") {
				s.Score *= 0.4
				best[n] = s
			}
		}
	}
	out := make([]Suggestion, 0, len(best))
	for _, s := range best {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].Skill < out[j].Skill
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// RouterText is everything the router says in tables: the playbook table in plan-investigation's SKILL.md and the symptom table in its reference/router.md.
func RouterText() string {
	body := ""
	if ms, err := All(); err == nil {
		for _, m := range ms {
			if m.Name == "plan-investigation" {
				body = m.Body
			}
		}
	}
	ref, _ := Reference("plan-investigation", "reference/router.md")
	return body + "\n" + ref
}
