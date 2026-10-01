package skilleval

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Verdict is the judge's reading of one expected behaviour.
type Verdict struct {
	Behavior string `json:"behavior"`
	Met      bool   `json:"met"`
	Why      string `json:"why,omitempty"`
}

// JudgePrompt asks a model to grade a run against the expected behaviours. It shows the steps and their results so
// the judge can check the answer against what the tools returned, not just how it reads.
func JudgePrompt(query string, expected []string, run Run) string {
	var b strings.Builder
	b.WriteString("You are grading one run of an AI agent. Be strict: mark a behaviour met only if the transcript clearly shows it.\n\n")
	fmt.Fprintf(&b, "USER QUERY (exactly what the agent was sent, including the network id and credentials note):\n%s\n\nEXPECTED BEHAVIOURS:\n", query)
	for i, e := range expected {
		fmt.Fprintf(&b, "%d. %s\n", i+1, e)
	}
	b.WriteString("\nTRANSCRIPT (tool calls, then what each returned):\n")
	for i, s := range run.Steps {
		fmt.Fprintf(&b, "[%d] %s: %s\n    -> %s\n", i+1, s.Tool, s.Input, s.Result)
	}
	fmt.Fprintf(&b, "\nFINAL ANSWER:\n%s\n\n", run.Final)
	b.WriteString(`Reply with JSON only, no prose: {"results":[{"n":1,"met":true,"why":"one short sentence"}, ...]} with one entry per expected behaviour, in order.`)
	return b.String()
}

// ParseVerdicts reads the judge's reply. A reply that does not parse, or that answers the wrong number of
// behaviours, is an error: an unreadable judgement must not count as a pass.
func ParseVerdicts(reply string, expected []string) ([]Verdict, error) {
	i, j := strings.Index(reply, "{"), strings.LastIndex(reply, "}")
	if i < 0 || j < i {
		return nil, fmt.Errorf("judge reply has no JSON object: %.120q", reply)
	}
	var doc struct {
		Results []struct {
			N   int    `json:"n"`
			Met bool   `json:"met"`
			Why string `json:"why"`
		} `json:"results"`
	}
	if err := json.Unmarshal([]byte(reply[i:j+1]), &doc); err != nil {
		return nil, fmt.Errorf("judge reply is not valid JSON: %w", err)
	}
	if len(doc.Results) != len(expected) {
		return nil, fmt.Errorf("judge answered %d behaviours, expected %d", len(doc.Results), len(expected))
	}
	out := make([]Verdict, len(expected))
	for k, r := range doc.Results {
		if r.N != k+1 {
			return nil, fmt.Errorf("judge answered out of order at position %d (n=%d)", k+1, r.N)
		}
		out[k] = Verdict{Behavior: expected[k], Met: r.Met, Why: r.Why}
	}
	return out, nil
}

// Outcome is the judge's reading of whether an answer is usable, independent of which skills were involved. It is
// what lets a run with the skills be compared with a run without them.
type Outcome struct {
	Answered bool   `json:"answered"`
	Grounded bool   `json:"grounded"`
	Why      string `json:"why,omitempty"`
	// GroundedNA is set by the runner, not the judge: the suite has no tool output to ground an answer in.
	GroundedNA bool `json:"grounded_na,omitempty"`
}

// OutcomePrompt asks whether the final answer answers the query and whether each claim in it is backed by what the
// tools returned.
func OutcomePrompt(query string, run Run) string {
	var b strings.Builder
	b.WriteString("You are grading one run of an AI agent that answers questions about a network by calling tools. Be strict.\n\n")
	fmt.Fprintf(&b, "USER QUERY (exactly what the agent was sent, including the network id and credentials note):\n%s\n\nTRANSCRIPT (tool calls, then what each returned):\n", query)
	for i, s := range run.Steps {
		fmt.Fprintf(&b, "[%d] %s: %s\n    -> %s\n", i+1, s.Tool, s.Input, s.Result)
	}
	fmt.Fprintf(&b, "\nFINAL ANSWER:\n%s\n\n", run.Final)
	b.WriteString("answered: the final answer gives the user something usable for the query, or correctly says why it cannot (an out-of-scope request declined with the reason counts).\n")
	b.WriteString("grounded: every specific fact, number or name in the answer appears in the tool results above, and where a result was unknown or empty the answer says so rather than filling the gap.\n\n")
	b.WriteString(`Reply with JSON only: {"answered":true,"grounded":true,"why":"one short sentence"}`)
	return b.String()
}

// ParseOutcome reads the judge's reply; an unreadable reply is an error, never a pass.
func ParseOutcome(reply string) (Outcome, error) {
	i, j := strings.Index(reply, "{"), strings.LastIndex(reply, "}")
	if i < 0 || j < i {
		return Outcome{}, fmt.Errorf("judge reply has no JSON object: %.120q", reply)
	}
	var o Outcome
	dec := json.NewDecoder(strings.NewReader(reply[i : j+1]))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&o); err != nil {
		return Outcome{}, fmt.Errorf("judge reply is not the expected JSON: %w", err)
	}
	return o, nil
}
