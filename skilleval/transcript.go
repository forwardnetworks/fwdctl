// Package skilleval runs the skill evaluations in evals/ against a real agent (the claude CLI, headless) and
// scores them: did the skill trigger when it should, did the run show the expected behaviours, and what did it cost.
package skilleval

import (
	"bufio"
	"encoding/json"
	"io"
	"regexp"
	"strings"
)

// Run is what one headless agent session did.
type Run struct {
	Skills       []string `json:"skills"`   // skills loaded through the Skill tool
	Commands     []string `json:"commands"` // Bash commands the agent ran
	ToolCalls    int      `json:"tool_calls"`
	Turns        int      `json:"turns"`
	CostUSD      float64  `json:"cost_usd"`
	DurationMS   int      `json:"duration_ms"`
	InputTokens  int      `json:"input_tokens"` // including cache reads and writes
	OutputTokens int      `json:"output_tokens"`
	Final        string   `json:"final"`
	Steps        []Step   `json:"steps"`
	Error        string   `json:"error,omitempty"`
}

// Step is one tool call and a bounded look at what came back.
type Step struct {
	Tool   string `json:"tool"`
	Input  string `json:"input"`
	Result string `json:"result"`
}

// Large enough for the biggest skill result (about 12 KB), so the judge can check an answer against the whole of it.
const maxResultChars = 16000

type event struct {
	Type    string `json:"type"`
	Subtype string `json:"subtype"`
	Message struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
	Result     string  `json:"result"`
	IsError    bool    `json:"is_error"`
	NumTurns   int     `json:"num_turns"`
	CostUSD    float64 `json:"total_cost_usd"`
	DurationMS int     `json:"duration_ms"`
	Usage      struct {
		Input       int `json:"input_tokens"`
		CacheCreate int `json:"cache_creation_input_tokens"`
		CacheRead   int `json:"cache_read_input_tokens"`
		Output      int `json:"output_tokens"`
	} `json:"usage"`
}

type block struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	Name      string          `json:"name"`
	ID        string          `json:"id"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
}

// ParseTranscript reads claude's stream-json output.
func ParseTranscript(r io.Reader) (Run, error) {
	var run Run
	byID := map[string]int{}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 1<<20), 1<<26)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var ev event
		if json.Unmarshal([]byte(line), &ev) != nil {
			continue
		}
		switch ev.Type {
		case "assistant", "user":
			var blocks []block
			if json.Unmarshal(ev.Message.Content, &blocks) != nil {
				continue
			}
			for _, b := range blocks {
				switch b.Type {
				case "tool_use":
					run.ToolCalls++
					in := string(b.Input)
					switch b.Name {
					case "Skill":
						var s struct {
							Skill string `json:"skill"`
						}
						_ = json.Unmarshal(b.Input, &s)
						run.Skills = append(run.Skills, s.Skill)
					case "Bash":
						var s struct {
							Command string `json:"command"`
						}
						_ = json.Unmarshal(b.Input, &s)
						run.Commands = append(run.Commands, s.Command)
						in = s.Command
					}
					byID[b.ID] = len(run.Steps)
					run.Steps = append(run.Steps, Step{Tool: b.Name, Input: clip(in, 600)})
				case "tool_result":
					if i, ok := byID[b.ToolUseID]; ok {
						run.Steps[i].Result = clip(flatten(b.Content), maxResultChars)
					}
				}
			}
		case "result":
			run.Final = ev.Result
			run.Turns = ev.NumTurns
			run.CostUSD = ev.CostUSD
			run.DurationMS = ev.DurationMS
			run.InputTokens = ev.Usage.Input + ev.Usage.CacheCreate + ev.Usage.CacheRead
			run.OutputTokens = ev.Usage.Output
			if ev.IsError || (ev.Subtype != "" && ev.Subtype != "success") {
				run.Error = ev.Subtype
			}
		}
	}
	return run, sc.Err()
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "...[truncated]"
}

// flatten turns a tool_result content (a string, or a list of text blocks) into text.
func flatten(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var bs []block
	if json.Unmarshal(raw, &bs) == nil {
		var parts []string
		for _, b := range bs {
			parts = append(parts, b.Text)
		}
		return strings.Join(parts, "\n")
	}
	return string(raw)
}

var cliUse = regexp.MustCompile(`fwdctl\s+(run|describe)\s+([a-z0-9-]+)`)

// Triggered reports whether the agent used a skill: it loaded it through the Skill tool, or ran or described it
// through the CLI. Both count, because a harness without a Skill tool reaches the skills through the CLI alone.
func (r Run) Triggered(skill string) bool {
	for _, s := range r.Skills {
		if s == skill {
			return true
		}
	}
	for _, c := range r.Commands {
		for _, m := range cliUse.FindAllStringSubmatch(c, -1) {
			if m[2] == skill {
				return true
			}
		}
	}
	return false
}

// UsedAnySkill reports whether the agent touched any forward skill at all.
func (r Run) UsedAnySkill() bool {
	if len(r.Skills) > 0 {
		return true
	}
	for _, c := range r.Commands {
		if cliUse.MatchString(c) {
			return true
		}
	}
	return false
}
