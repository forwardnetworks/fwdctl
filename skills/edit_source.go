package skills

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/forwardnetworks/fwdctl/fwd"
	"github.com/forwardnetworks/fwdctl/result"
)

const editSourceName = "edit-source"

func init() { Register(editSourceName, editSource) }

type editSourceInput struct {
	NetworkID  string          `json:"network_id"`
	Object     string          `json:"object"`
	Action     string          `json:"action"`
	Name       string          `json:"name"`
	Definition json.RawMessage `json:"definition"`
	SecretFile string          `json:"secret_file"`
	SecretEnv  string          `json:"secret_env"`
	Confirm    string          `json:"confirm"`
	Apply      bool            `json:"apply"`
}

// secretKeyNamesInDefinition returns the keys in a definition whose names say they hold a secret: a secret is never typed into the input JSON (shell history, agent transcripts and
// logs keep it), it comes from secret_file or secret_env. A key that ends in Id is an identifier, not a secret.
func secretKeyNamesInDefinition(raw json.RawMessage) []string {
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return nil
	}
	var bad []string
	var walk func(x any)
	walk = func(x any) {
		switch m := x.(type) {
		case map[string]any:
			for k, val := range m {
				if fwd.SecretKey(k) && !strings.HasSuffix(strings.ToLower(k), "id") {
					bad = append(bad, k)
				}
				walk(val)
			}
		case []any:
			for _, e := range m {
				walk(e)
			}
		}
	}
	walk(v)
	return bad
}

// sourceSecrets reads the secret for a write once: a file (mode 600) or an environment variable. The text is either the one secret or, for an object with several (SNMP v3), a JSON
// object of named secrets; need names them in order and the first is what a plain text secret means.
func sourceSecrets(in editSourceInput, need ...string) (map[string]fwd.Secret, error) {
	sec, err := fwd.ReadSecret(in.SecretFile, in.SecretEnv)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	out := map[string]fwd.Secret{}
	text := strings.TrimSpace(sec.Reveal())
	if strings.HasPrefix(text, "{") {
		var m map[string]string
		if json.Unmarshal([]byte(text), &m) != nil {
			return nil, fmt.Errorf("%w: the secret looks like JSON but does not parse as an object of strings", ErrInvalidInput)
		}
		for _, n := range need {
			if v, ok := m[n]; ok && v != "" {
				out[n] = fwd.SecretFromString(v)
			}
		}
		return out, nil
	}
	if len(need) > 0 {
		out[need[0]] = sec
	}
	return out, nil
}

func editSource(ctx context.Context, s *fwd.Session, raw json.RawMessage) (result.Result, error) {
	var in editSourceInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return result.Result{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	if in.NetworkID == "" {
		return result.Result{}, fmt.Errorf("%w: network_id is required", ErrInvalidInput)
	}
	if bad := secretKeyNamesInDefinition(in.Definition); len(bad) > 0 {
		return result.Result{}, fmt.Errorf("%w: definition has secret-looking field(s) %s: a secret is never put in the input; give secret_file (a path, mode 600) or secret_env (an environment variable name)", ErrInvalidInput, strings.Join(bad, ", "))
	}
	cx := result.Context{NetworkID: in.NetworkID, Scope: "account", State: "current"}
	var plan *networkPlan
	var err error
	switch in.Object {
	case "credential":
		plan, err = planCredential(ctx, s, in)
	case "jump_server":
		plan, err = planJumpServer(ctx, s, in)
	case "proxy":
		plan, err = planProxy(ctx, s, in)
	case "classic_device":
		plan, err = planClassicDevice(ctx, s, in)
	case "endpoint":
		plan, err = planEndpoint(ctx, s, in)
	case "schedule":
		plan, err = planSchedule(ctx, s, in)
	case "cloud_account":
		plan, err = planCloudAccount(ctx, s, in)
	case "rapid7_source":
		plan, err = planRapid7(ctx, s, in)
	case "tenable_source":
		plan, err = planTenable(ctx, s, in)
	case "controller_setup":
		plan, err = planControllerSetup(ctx, s, in)
	case "mist_setup":
		plan, err = planMistSetup(ctx, s, in)
	case "custom_command_group":
		plan, err = planCustomCommandGroup(ctx, s, in)
	case "discovery":
		plan, err = planDiscovery(ctx, s, in)
	default:
		return result.Result{}, fmt.Errorf("%w: object must be credential, jump_server, proxy, classic_device, endpoint, schedule, cloud_account, rapid7_source, tenable_source, controller_setup, mist_setup, custom_command_group or discovery", ErrInvalidInput)
	}
	if err != nil {
		return result.Result{}, err
	}
	return finishPlan(ctx, editSourceName, cx, plan, in.Object, in.Action, in.Apply, in.Confirm)
}

func noSecretInputs(in editSourceInput) error {
	if in.SecretFile != "" || in.SecretEnv != "" {
		return fmt.Errorf("%w: this action takes no secret", ErrInvalidInput)
	}
	return nil
}
