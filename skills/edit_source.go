package skills

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	forward "github.com/forwardnetworks/forward-go-sdk"

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
	case "schedule":
		plan, err = planSchedule(ctx, s, in)
	case "cloud_account":
		plan, err = planCloudAccount(ctx, s, in)
	case "rapid7_source":
		plan, err = planRapid7(ctx, s, in)
	case "controller_setup":
		plan, err = planControllerSetup(ctx, s, in)
	case "mist_setup":
		plan, err = planMistSetup(ctx, s, in)
	default:
		return result.Result{}, fmt.Errorf("%w: object must be credential, jump_server, proxy, classic_device, schedule, cloud_account, rapid7_source, controller_setup or mist_setup", ErrInvalidInput)
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

func planCredential(ctx context.Context, s *fwd.Session, in editSourceInput) (*networkPlan, error) {
	var def struct {
		Type           string `json:"type"`
		Name           string `json:"name"`
		Username       string `json:"username"`
		Kind           string `json:"kind"`
		LoginType      string `json:"loginType"`
		PrivilegeLevel *int32 `json:"privilegeLevel"`
		AutoAssociate  *bool  `json:"autoAssociate"`
		PrivilegedID   string `json:"privilegedModePasswordId"`
		Version        string `json:"version"`
		Port           *int   `json:"port"`
		TimeoutSec     *int   `json:"timeoutSec"`
		AuthType       string `json:"authType"`
		PrivacyProto   string `json:"privacyProtocol"`
		AuthUsername   string `json:"authUsername"`
	}
	kind := ""
	if len(in.Definition) > 0 {
		if err := decodeDefinition(in.Definition, &def, "type (CLI|HTTP|SNMP), name, username, kind, loginType, privilegeLevel, autoAssociate, privilegedModePasswordId, version, port, timeoutSec, authType, privacyProtocol, authUsername"); err != nil {
			return nil, err
		}
		kind = strings.ToUpper(def.Type)
	}
	switch in.Action {
	case "delete":
		// name is the credential id here; kind comes from definition.type
		if in.Name == "" || kind == "" {
			return nil, fmt.Errorf("%w: delete needs name (the credential id, from inspect-platform area credentials) and definition {\"type\": \"CLI\"|\"HTTP\"|\"SNMP\"}", ErrInvalidInput)
		}
		if err := noSecretInputs(in); err != nil {
			return nil, err
		}
		return &networkPlan{target: fmt.Sprintf("delete %s credential %s from network %s", kind, in.Name, in.NetworkID), action: "delete_credential", before: map[string]any{"id": in.Name, "type": kind}, after: nil, reversible: false, confirm: in.Name,
			undo:   "none: the secret is not kept anywhere Forward returns it; create the credential again with the secret",
			limits: []string{"a device that uses this credential can no longer be collected until it has another"},
			do: func(ctx context.Context) error {
				var err error
				switch kind {
				case "CLI":
					_, err = s.Client.Credentials.DeleteCLI(ctx, in.NetworkID, in.Name)
				case "HTTP":
					_, err = s.Client.Credentials.DeleteHTTP(ctx, in.NetworkID, in.Name)
				case "SNMP":
					_, err = s.Client.Credentials.DeleteSNMP(ctx, in.NetworkID, in.Name)
				default:
					err = fmt.Errorf("%w: type must be CLI, HTTP or SNMP", ErrInvalidInput)
				}
				return err
			}}, nil
	case "create":
		if strings.TrimSpace(def.Name) == "" || kind == "" {
			return nil, fmt.Errorf("%w: create needs definition.type (CLI, HTTP or SNMP) and definition.name", ErrInvalidInput)
		}
		shown := map[string]any{"type": kind, "name": def.Name, "username": nilIfEmpty(def.Username), "secret": "<secret>"}
		plan := &networkPlan{target: fmt.Sprintf("create %s credential %q on network %s", kind, def.Name, in.NetworkID), action: "create_credential", before: nil, after: shown, reversible: true,
			undo:   "delete the new credential (edit-source object credential action delete, name its id, confirm its id)",
			limits: []string{"the secret is read from " + secretSource(in) + " and is never shown, logged or returned; Forward returns only an id for it"}}
		// a dry run must not need the secret file to exist, but must tell when it would not work
		if in.Apply {
			var need []string
			switch kind {
			case "CLI", "HTTP":
				need = []string{"password"}
			case "SNMP":
				need = []string{"communityString", "password", "privacyPassword"}
			}
			secs, err := sourceSecrets(in, need...)
			if err != nil {
				return nil, err
			}
			switch kind {
			case "CLI":
				req := forward.CLICredentialRequest{Type: def.Kind, Name: def.Name, Username: def.Username, Password: secs["password"].Reveal(), PrivilegedModePasswordID: def.PrivilegedID, PrivilegeLevel: def.PrivilegeLevel, AutoAssociate: def.AutoAssociate}
				plan.do = func(ctx context.Context) error {
					_, _, err := s.Client.Credentials.CreateCLI(ctx, in.NetworkID, req)
					return err
				}
			case "HTTP":
				req := forward.HTTPCredentialRequest{Type: def.Kind, Name: def.Name, Username: def.Username, Password: secs["password"].Reveal(), LoginType: def.LoginType, AutoAssociate: def.AutoAssociate}
				plan.do = func(ctx context.Context) error {
					_, _, err := s.Client.Credentials.CreateHTTP(ctx, in.NetworkID, req)
					return err
				}
			case "SNMP":
				req := forward.SNMPCredentialRequest{Name: def.Name, Version: forward.SNMPVersion(strings.ToUpper(def.Version)), Port: def.Port, TimeoutSec: def.TimeoutSec, AutoAssociate: def.AutoAssociate}
				if req.Version == forward.SNMPVersionV3 {
					req.AuthSettings = &forward.SNMPAuthSettings{Username: firstNonEmpty(def.AuthUsername, def.Username), Password: secs["password"].Reveal(), AuthType: forward.SNMPAuthType(strings.ToUpper(def.AuthType)),
						PrivacyProtocol: forward.SNMPPrivacyProtocol(strings.ToUpper(def.PrivacyProto)), PrivacyPassword: secs["privacyPassword"].Reveal()}
				} else {
					req.CommunityString = secs["communityString"].Reveal()
				}
				plan.do = func(ctx context.Context) error {
					_, _, err := s.Client.Credentials.CreateSNMP(ctx, in.NetworkID, req)
					return err
				}
			default:
				return nil, fmt.Errorf("%w: type must be CLI, HTTP or SNMP", ErrInvalidInput)
			}
			plan.verify = func(ctx context.Context) (bool, any, error) {
				return credentialExists(ctx, s, in.NetworkID, kind, def.Name)
			}
		}
		if kind != "CLI" && kind != "HTTP" && kind != "SNMP" {
			return nil, fmt.Errorf("%w: type must be CLI, HTTP or SNMP", ErrInvalidInput)
		}
		return plan, nil
	}
	return nil, fmt.Errorf("%w: object credential takes action create or delete", ErrInvalidInput)
}

func secretSource(in editSourceInput) string {
	if in.SecretEnv != "" {
		return "the environment variable " + in.SecretEnv
	}
	if in.SecretFile != "" {
		return "the file " + in.SecretFile
	}
	return "secret_file or secret_env (not given yet: apply needs one)"
}

func credentialExists(ctx context.Context, s *fwd.Session, network, kind, name string) (bool, any, error) {
	var names []string
	switch kind {
	case "CLI":
		l, _, err := s.Client.Credentials.ListCLI(ctx, network)
		if err != nil {
			return false, nil, err
		}
		for _, c := range l {
			names = append(names, c.Name)
		}
	case "HTTP":
		l, _, err := s.Client.Credentials.ListHTTP(ctx, network)
		if err != nil {
			return false, nil, err
		}
		for _, c := range l {
			names = append(names, c.Name)
		}
	case "SNMP":
		l, _, err := s.Client.Credentials.ListSNMP(ctx, network)
		if err != nil {
			return false, nil, err
		}
		for _, c := range l {
			names = append(names, c.Name)
		}
	}
	for _, n := range names {
		if n == name {
			return true, nil, nil
		}
	}
	return false, map[string]any{"names": names}, nil
}

func planJumpServer(ctx context.Context, s *fwd.Session, in editSourceInput) (*networkPlan, error) {
	if in.Action != "create" {
		return nil, fmt.Errorf("%w: object jump_server takes action create (the API has no update or delete)", ErrInvalidInput)
	}
	var def struct {
		Host     string `json:"host"`
		Port     int    `json:"port"`
		Username string `json:"username"`
		Auth     string `json:"auth"`
		SSHCert  string `json:"sshCert"`
	}
	if err := decodeDefinition(in.Definition, &def, "host, port, username, auth (key|password), sshCert"); err != nil {
		return nil, err
	}
	auth := strings.ToLower(def.Auth)
	if def.Host == "" || def.Username == "" || (auth != "key" && auth != "password") {
		return nil, fmt.Errorf("%w: create needs host, username and auth (key or password)", ErrInvalidInput)
	}
	cur, _, err := s.Client.JumpServers.List(ctx, in.NetworkID)
	if err != nil {
		return nil, err
	}
	for _, j := range cur {
		if strings.EqualFold(j.Host, def.Host) && j.Port == max(def.Port, 22) && j.Username == def.Username {
			return nil, fmt.Errorf("%w: that jump server (%s@%s) already exists", ErrInvalidInput, def.Username, def.Host)
		}
	}
	plan := &networkPlan{target: fmt.Sprintf("create a %s-authenticated jump server %s@%s on network %s", auth, def.Username, def.Host, in.NetworkID), action: "create_jump_server", before: nil,
		after: map[string]any{"host": def.Host, "port": max(def.Port, 22), "username": def.Username, "auth": auth, "secret": "<secret>"}, reversible: false,
		undo:   "none through the API: the SDK has no jump server delete (remove it in the Forward UI)",
		limits: []string{"the " + map[string]string{"key": "private key", "password": "password"}[auth] + " is read from " + secretSource(in) + " and is never shown, logged or returned"}}
	if in.Apply {
		secs, err := sourceSecrets(in, map[string]string{"key": "sshKey", "password": "password"}[auth])
		if err != nil {
			return nil, err
		}
		if auth == "key" {
			req := forward.JumpServerRequest{Host: def.Host, Port: def.Port, Username: def.Username, SSHKey: secs["sshKey"].Reveal(), SSHCert: def.SSHCert}
			plan.do = func(ctx context.Context) error {
				_, _, err := s.Client.JumpServers.Create(ctx, in.NetworkID, req)
				return err
			}
		} else {
			req := forward.JumpServerPasswordRequest{Host: def.Host, Port: def.Port, Username: def.Username, Password: secs["password"].Reveal(), SupportsPortForwarding: true}
			plan.do = func(ctx context.Context) error {
				_, _, err := s.Client.JumpServers.CreateWithPassword(ctx, in.NetworkID, req)
				return err
			}
		}
		plan.verify = func(ctx context.Context) (bool, any, error) {
			l, _, e := s.Client.JumpServers.List(ctx, in.NetworkID)
			for _, j := range l {
				if strings.EqualFold(j.Host, def.Host) && j.Username == def.Username {
					return true, nil, e
				}
			}
			return false, nil, e
		}
	}
	return plan, nil
}

func planProxy(ctx context.Context, s *fwd.Session, in editSourceInput) (*networkPlan, error) {
	if err := noSecretInputs(in); err != nil {
		return nil, err
	}
	var def forward.ProxyServer
	if err := decodeDefinition(in.Definition, &def, "name, host, port, protocol, disableCertChecking"); err != nil {
		return nil, err
	}
	cur, _, err := s.Client.Proxies.List(ctx, in.NetworkID)
	if err != nil {
		return nil, err
	}
	switch in.Action {
	case "create":
		if def.Name == "" || def.Host == "" || def.Port == 0 || def.Protocol == "" {
			return nil, fmt.Errorf("%w: create needs name, host, port and protocol", ErrInvalidInput)
		}
		for _, p := range cur {
			if p.Name == def.Name {
				return nil, fmt.Errorf("%w: a proxy named %q already exists (id %s)", ErrInvalidInput, p.Name, p.ID)
			}
		}
		return &networkPlan{target: fmt.Sprintf("create proxy %q (%s://%s:%d) on network %s", def.Name, def.Protocol, def.Host, def.Port, in.NetworkID), action: "create_proxy", before: nil, after: def, reversible: false,
			undo: "none through the API: the SDK has no proxy delete (remove it in the Forward UI, or update it with action update)",
			do: func(ctx context.Context) error {
				_, _, err := s.Client.Proxies.Create(ctx, in.NetworkID, def)
				return err
			},
			verify: func(ctx context.Context) (bool, any, error) {
				l, _, e := s.Client.Proxies.List(ctx, in.NetworkID)
				for _, p := range l {
					if p.Name == def.Name {
						return true, nil, e
					}
				}
				return false, nil, e
			}}, nil
	case "update":
		if in.Name == "" {
			return nil, fmt.Errorf("%w: update needs name (the proxy id, from inspect-platform area proxies)", ErrInvalidInput)
		}
		for _, p := range cur {
			if string(p.ID) == in.Name {
				return &networkPlan{target: fmt.Sprintf("replace proxy %q (%s) on network %s", p.Name, p.ID, in.NetworkID), action: "update_proxy", before: p, after: def, reversible: true, undo: "update it again with the before values",
					limits: []string{"update REPLACES the proxy's fields: restate every field"},
					do: func(ctx context.Context) error {
						_, _, err := s.Client.Proxies.Update(ctx, in.NetworkID, in.Name, def)
						return err
					}}, nil
			}
		}
		return nil, nil
	}
	return nil, fmt.Errorf("%w: object proxy takes action create or update", ErrInvalidInput)
}

func planClassicDevice(ctx context.Context, s *fwd.Session, in editSourceInput) (*networkPlan, error) {
	if err := noSecretInputs(in); err != nil {
		return nil, err
	}
	cur, _, err := s.Client.ClassicDevices.List(ctx, in.NetworkID)
	if err != nil {
		return nil, err
	}
	find := func(name string) *forward.ClassicDevice {
		for i := range cur {
			if cur[i].Name == name {
				return &cur[i]
			}
		}
		return nil
	}
	type devDef struct {
		Name             string `json:"name"`
		Host             string `json:"host"`
		Type             string `json:"type"`
		Port             *int32 `json:"port"`
		CLICredentialID  string `json:"cliCredentialId"`
		CLICredential2ID string `json:"cliCredential2Id"`
		CLICredential3ID string `json:"cliCredential3Id"`
		HTTPCredentialID string `json:"httpCredentialId"`
		SNMPCredentialID string `json:"snmpCredentialId"`
		JumpServerID     string `json:"jumpServerId"`
		Collect          *bool  `json:"collect"`
		Note             string `json:"note"`
	}
	fields := "name, host, type, port, cliCredentialId, cliCredential2Id, cliCredential3Id, httpCredentialId, snmpCredentialId, jumpServerId, collect, note"
	switch in.Action {
	case "delete":
		if in.Name == "" || len(in.Definition) > 0 {
			return nil, fmt.Errorf("%w: delete takes name (the device) and confirm only", ErrInvalidInput)
		}
		d := find(in.Name)
		if d == nil {
			return nil, nil
		}
		before, _ := fwd.Generic(d)
		return &networkPlan{target: fmt.Sprintf("delete device %q from the collection list of network %s", in.Name, in.NetworkID), action: "delete_device", before: before, after: nil, reversible: true, confirm: in.Name,
			undo:   "add it again with the before values (edit-source object classic_device action create)",
			limits: []string{"removing a device from the list stops it being collected; its past snapshots keep it. This changes the list of sources, not the device"},
			do: func(ctx context.Context) error {
				_, err := s.Client.ClassicDevices.Delete(ctx, in.NetworkID, in.Name)
				return err
			},
			verify: func(ctx context.Context) (bool, any, error) {
				l, _, e := s.Client.ClassicDevices.List(ctx, in.NetworkID)
				for _, x := range l {
					if x.Name == in.Name {
						return false, map[string]any{"still_listed": true}, e
					}
				}
				return true, nil, e
			}}, nil
	case "create":
		var def devDef
		if err := decodeDefinition(in.Definition, &def, fields); err != nil {
			return nil, err
		}
		if def.Name == "" || def.Host == "" {
			return nil, fmt.Errorf("%w: create needs name and host", ErrInvalidInput)
		}
		if find(def.Name) != nil {
			return nil, fmt.Errorf("%w: a device named %q is already in the list", ErrInvalidInput, def.Name)
		}
		req := forward.ClassicDeviceRequest{Name: def.Name, Host: def.Host, Type: def.Type, Port: def.Port, CLICredentialID: def.CLICredentialID, CLICredential2ID: def.CLICredential2ID, CLICredential3ID: def.CLICredential3ID,
			HTTPCredentialID: def.HTTPCredentialID, SNMPCredentialID: def.SNMPCredentialID, JumpServerID: def.JumpServerID, Collect: def.Collect, Note: def.Note}
		return &networkPlan{target: fmt.Sprintf("add device %q (%s) to the collection list of network %s", def.Name, def.Host, in.NetworkID), action: "add_device", before: nil, after: def, reversible: true,
			undo:   "delete it from the list (edit-source object classic_device action delete)",
			limits: []string{"credentials and the jump server are referred to by id (inspect-platform areas credentials, jump_servers); it is collected from the next collection, which edit-collection can start"},
			do: func(ctx context.Context) error {
				_, _, err := s.Client.ClassicDevices.Create(ctx, in.NetworkID, req)
				return err
			},
			verify: func(ctx context.Context) (bool, any, error) {
				l, _, e := s.Client.ClassicDevices.List(ctx, in.NetworkID)
				for _, x := range l {
					if x.Name == def.Name {
						return true, nil, e
					}
				}
				return false, nil, e
			}}, nil
	case "update":
		var def devDef
		if err := decodeDefinition(in.Definition, &def, fields); err != nil {
			return nil, err
		}
		d := find(in.Name)
		if d == nil {
			return nil, nil
		}
		patch := forward.ClassicDevicePatch{Port: def.Port, Collect: def.Collect}
		set := func(v string, to **string) {
			if v != "" {
				x := v
				*to = &x
			}
		}
		set(def.Host, &patch.Host)
		set(def.Type, &patch.Type)
		set(def.CLICredentialID, &patch.CLICredentialID)
		set(def.HTTPCredentialID, &patch.HTTPCredentialID)
		set(def.Note, &patch.Note)
		if def.SNMPCredentialID != "" || def.JumpServerID != "" || def.CLICredential2ID != "" || def.CLICredential3ID != "" {
			return nil, fmt.Errorf("%w: update can change host, type, port, cliCredentialId, httpCredentialId, collect and note; to change the SNMP credential, jump server or second and third CLI credentials, delete the device and add it again", ErrInvalidInput)
		}
		before, _ := fwd.Generic(d)
		return &networkPlan{target: fmt.Sprintf("update device %q on network %s", in.Name, in.NetworkID), action: "update_device", before: before, after: def, reversible: true, undo: "update it again with the before values",
			do: func(ctx context.Context) error {
				_, _, err := s.Client.ClassicDevices.Patch(ctx, in.NetworkID, in.Name, patch)
				return err
			}}, nil
	}
	return nil, fmt.Errorf("%w: object classic_device takes action create, update or delete", ErrInvalidInput)
}

func planSchedule(ctx context.Context, s *fwd.Session, in editSourceInput) (*networkPlan, error) {
	if err := noSecretInputs(in); err != nil {
		return nil, err
	}
	cur, _, err := s.Client.CollectionSchedules.List(ctx, in.NetworkID)
	if err != nil {
		return nil, err
	}
	var def struct {
		Enabled         *bool    `json:"enabled"`
		TimeZone        string   `json:"timeZone"`
		DaysOfTheWeek   []int    `json:"daysOfTheWeek"`
		Times           []string `json:"times"`
		PeriodInSeconds *int     `json:"periodInSeconds"`
		StartAt         string   `json:"startAt"`
		EndAt           string   `json:"endAt"`
	}
	fields := "enabled, timeZone, daysOfTheWeek (0 Sunday to 6 Saturday), times (HH:mm) or periodInSeconds, startAt, endAt (HH:mm, with a period)"
	toDef := func() forward.CollectionScheduleDefinition {
		en := true
		if def.Enabled != nil {
			en = *def.Enabled
		}
		return forward.CollectionScheduleDefinition{Enabled: en, TimeZone: def.TimeZone, DaysOfTheWeek: def.DaysOfTheWeek, Times: def.Times, PeriodInSeconds: def.PeriodInSeconds, StartAt: def.StartAt, EndAt: def.EndAt}
	}
	byID := func(id string) *forward.CollectionSchedule {
		for i := range cur {
			if string(cur[i].ID) == id {
				return &cur[i]
			}
		}
		return nil
	}
	limits := []string{"a collector runs one network collection at a time, so a scheduled run waits if the previous one is still going; the next run time is not returned by Forward", "Forward accepts only its own list of time zones (UTC and regional IDs such as America/Chicago) and answers 400 otherwise; a duplicate schedule is also a 400"}
	switch in.Action {
	case "create":
		if err := decodeDefinition(in.Definition, &def, fields); err != nil {
			return nil, err
		}
		d := toDef()
		return &networkPlan{target: fmt.Sprintf("add a collection schedule to network %s", in.NetworkID), action: "create_schedule", before: cur, after: d, reversible: true, undo: "delete the new schedule (edit-source object schedule action delete, name its id)", limits: limits,
			do: func(ctx context.Context) error {
				_, _, err := s.Client.CollectionSchedules.Create(ctx, in.NetworkID, d)
				return err
			},
			verify: func(ctx context.Context) (bool, any, error) {
				l, _, e := s.Client.CollectionSchedules.List(ctx, in.NetworkID)
				return len(l) > len(cur), nil, e
			}}, nil
	case "update":
		if err := decodeDefinition(in.Definition, &def, fields); err != nil {
			return nil, err
		}
		c := byID(in.Name)
		if c == nil {
			return nil, nil
		}
		d := toDef()
		return &networkPlan{target: fmt.Sprintf("replace collection schedule %s on network %s", in.Name, in.NetworkID), action: "replace_schedule", before: c, after: d, reversible: true, undo: "replace it again with the before values",
			limits: append(limits, "replace restates EVERY field: anything not given is dropped"),
			do: func(ctx context.Context) error {
				_, err := s.Client.CollectionSchedules.Replace(ctx, in.NetworkID, in.Name, d)
				return err
			}}, nil
	case "delete":
		c := byID(in.Name)
		if c == nil {
			return nil, nil
		}
		return &networkPlan{target: fmt.Sprintf("delete collection schedule %s from network %s", in.Name, in.NetworkID), action: "delete_schedule", before: c, after: nil, reversible: true, confirm: in.Name,
			undo:   "create it again with the before values",
			limits: append(limits, "with no schedule left the network is collected only when started by hand (edit-collection)"),
			do: func(ctx context.Context) error {
				_, err := s.Client.CollectionSchedules.Delete(ctx, in.NetworkID, in.Name)
				return err
			},
			verify: func(ctx context.Context) (bool, any, error) {
				l, _, e := s.Client.CollectionSchedules.List(ctx, in.NetworkID)
				for _, x := range l {
					if string(x.ID) == in.Name {
						return false, nil, e
					}
				}
				return true, nil, e
			}}, nil
	}
	return nil, fmt.Errorf("%w: object schedule takes action create, update or delete", ErrInvalidInput)
}

func planCloudAccount(ctx context.Context, s *fwd.Session, in editSourceInput) (*networkPlan, error) {
	cur, _, err := s.Client.CloudAccounts.List(ctx, in.NetworkID)
	if err != nil {
		return nil, err
	}
	find := func(name string) *forward.CloudAccount {
		for i := range cur {
			if cur[i].Name == name {
				return &cur[i]
			}
		}
		return nil
	}
	var def struct {
		Type            string         `json:"type"`
		Name            string         `json:"name"`
		Collect         *bool          `json:"collect"`
		ProxyServerID   string         `json:"proxyServerId"`
		Regions         map[string]int `json:"regions"`
		Username        string         `json:"username"`
		ClientID        string         `json:"clientId"`
		ClientEmail     string         `json:"clientEmail"`
		PrivateKeyID    string         `json:"privateKeyId"`
		Tenant          string         `json:"tenant"`
		Environment     string         `json:"environment"`
		SubscriptionIDs []string       `json:"subscriptionIds"`
		Concurrency     *int64         `json:"concurrency"`
	}
	fields := "type (AWS, AZURE, GCP, ...), name, collect, proxyServerId, regions, username (an AWS access key id), clientId, clientEmail, privateKeyId, tenant, environment, subscriptionIds, concurrency"
	// the credential half: the secret names each provider needs, read from the secret file
	credentialOf := func(secs map[string]fwd.Secret) forward.CloudAccountCredentialRequest {
		return forward.CloudAccountCredentialRequest{Type: strings.ToUpper(def.Type), Username: def.Username, Password: secs["password"].Reveal(), ClientID: def.ClientID, Tenant: def.Tenant,
			ClientEmail: def.ClientEmail, PrivateKeyID: def.PrivateKeyID, PrivateKey: secs["privateKey"].Reveal(), APIKey: secs["apiKey"].Reveal()}
	}
	secretNames := []string{"password", "privateKey", "apiKey"}
	switch in.Action {
	case "delete":
		if in.Name == "" {
			return nil, fmt.Errorf("%w: delete needs name (the account)", ErrInvalidInput)
		}
		if err := noSecretInputs(in); err != nil {
			return nil, err
		}
		a := find(in.Name)
		if a == nil {
			return nil, nil
		}
		before, _ := fwd.Generic(a)
		return &networkPlan{target: fmt.Sprintf("delete cloud account %q from network %s", in.Name, in.NetworkID), action: "delete_cloud_account", before: before, after: nil, reversible: false, confirm: in.Name,
			undo:   "none: the stored keys are gone; create the account again with its credentials",
			limits: []string{"its VPCs and instances disappear from the next snapshot; past snapshots keep them"},
			do: func(ctx context.Context) error {
				_, err := s.Client.CloudAccounts.Delete(ctx, in.NetworkID, in.Name)
				return err
			},
			verify: func(ctx context.Context) (bool, any, error) {
				l, _, e := s.Client.CloudAccounts.List(ctx, in.NetworkID)
				for _, x := range l {
					if x.Name == in.Name {
						return false, nil, e
					}
				}
				return true, nil, e
			}}, nil
	case "test":
		if in.Name == "" || find(in.Name) == nil {
			return nil, nil
		}
		if err := noSecretInputs(in); err != nil {
			return nil, err
		}
		return &networkPlan{target: fmt.Sprintf("test the connection of cloud account %q on network %s", in.Name, in.NetworkID), action: "test_cloud_account", before: nil, after: "Forward records the result per region", reversible: true, undo: "none needed: a test changes only the recorded test result",
			limits: []string{"the test calls the cloud provider from the collector; read the result with inspect-collection view config"},
			do: func(ctx context.Context) error {
				_, err := s.Client.CloudAccounts.Test(ctx, in.NetworkID, in.Name)
				return err
			}}, nil
	case "create", "rotate":
		if err := decodeDefinition(in.Definition, &def, fields); err != nil {
			return nil, err
		}
		kind := strings.ToUpper(def.Type)
		if kind == "" {
			return nil, fmt.Errorf("%w: definition.type is required (AWS, AZURE, GCP, ...)", ErrInvalidInput)
		}
		if in.Action == "create" {
			if def.Name == "" {
				return nil, fmt.Errorf("%w: create needs definition.name", ErrInvalidInput)
			}
			if find(def.Name) != nil {
				return nil, fmt.Errorf("%w: an account named %q already exists on the network", ErrInvalidInput, def.Name)
			}
		} else if in.Name == "" || find(in.Name) == nil {
			return nil, nil
		}
		name := firstNonEmpty(in.Name, def.Name)
		verb, act := "create", "create_cloud_account"
		undo := "delete the new account (edit-source object cloud_account action delete, confirm its name)"
		if in.Action == "rotate" {
			verb, act, undo = "replace the stored credentials of", "rotate_cloud_account_credentials", "none: the earlier secret is not kept by Forward; enter it again to go back"
		}
		plan := &networkPlan{target: fmt.Sprintf("%s cloud account %q (%s) on network %s", verb, name, kind, in.NetworkID), action: act, before: nil,
			after: map[string]any{"type": kind, "name": name, "collect": def.Collect, "regions": def.Regions, "secret": "<secret>"}, reversible: in.Action == "create", undo: undo,
			limits: []string{"the secret is read from " + secretSource(in) + " (plain text is the password/secret key; a JSON object may carry password, privateKey or apiKey) and is never shown, logged or returned", "the account is tested with action test; regions are collected from the next collection"}}
		if in.Action == "create" {
			collect := true
			if def.Collect != nil {
				collect = *def.Collect
			}
			if in.Apply {
				secs, err := sourceSecrets(in, secretNames...)
				if err != nil {
					return nil, err
				}
				cred := credentialOf(secs)
				req := forward.CloudAccountRequest{Type: kind, Name: def.Name, Collect: collect, ProxyServerID: def.ProxyServerID, Regions: def.Regions, Username: cred.Username, Password: cred.Password,
					ClientID: def.ClientID, Tenant: def.Tenant, Environment: def.Environment, SubscriptionIDs: def.SubscriptionIDs, Concurrency: def.Concurrency}
				if kind == "GCP" {
					req.Extra = map[string]any{"clientEmail": cred.ClientEmail, "privateKeyId": cred.PrivateKeyID, "privateKey": cred.PrivateKey}
				} else if cred.APIKey != "" {
					req.Extra = map[string]any{"apiKey": cred.APIKey}
				}
				plan.do = func(ctx context.Context) error {
					_, _, err := s.Client.CloudAccounts.Create(ctx, in.NetworkID, req)
					return err
				}
				plan.verify = func(ctx context.Context) (bool, any, error) {
					l, _, e := s.Client.CloudAccounts.List(ctx, in.NetworkID)
					for _, x := range l {
						if x.Name == def.Name {
							return true, nil, e
						}
					}
					return false, nil, e
				}
			}
			return plan, nil
		}
		if in.Apply {
			secs, err := sourceSecrets(in, secretNames...)
			if err != nil {
				return nil, err
			}
			cred := credentialOf(secs)
			plan.do = func(ctx context.Context) error {
				_, err := s.Client.CloudAccounts.UpdateCredential(ctx, in.NetworkID, name, cred)
				return err
			}
		}
		return plan, nil
	}
	return nil, fmt.Errorf("%w: object cloud_account takes action create, rotate, test or delete", ErrInvalidInput)
}

// planRapid7 creates or updates a Rapid7 vulnerability source of the network. Its login is a credential that already exists (credential_id), so no secret is read here.
func planRapid7(ctx context.Context, s *fwd.Session, in editSourceInput) (*networkPlan, error) {
	if err := noSecretInputs(in); err != nil {
		return nil, err
	}
	var def struct {
		Name                 string   `json:"name"`
		BaseURL              string   `json:"baseUrl"`
		CredentialID         string   `json:"credentialId"`
		DisableSSLValidation bool     `json:"disableSslValidation"`
		CollectionDisabled   bool     `json:"collectionDisabled"`
		ReportNames          []string `json:"reportNames"`
	}
	if err := decodeDefinition(in.Definition, &def, "name, baseUrl, credentialId, disableSslValidation, collectionDisabled, reportNames"); err != nil {
		return nil, err
	}
	cur, _, err := s.Client.Integrations.ListRapid7(ctx, in.NetworkID)
	if err != nil {
		return nil, err
	}
	req := forward.Rapid7SourceRequest{Name: def.Name, BaseURL: def.BaseURL, DisableSSLValidation: def.DisableSSLValidation, CredentialID: forward.Identifier(def.CredentialID),
		CollectionDisabled: def.CollectionDisabled, ReportNames: def.ReportNames}
	shown := func(name string) func(context.Context) (bool, any, error) {
		return func(ctx context.Context) (bool, any, error) {
			l, _, e := s.Client.Integrations.ListRapid7(ctx, in.NetworkID)
			for _, x := range l {
				if x.Name == name {
					g, _ := fwd.Generic(x)
					return x.BaseURL == def.BaseURL && x.CollectionDisabled == def.CollectionDisabled, g, e
				}
			}
			return false, nil, e
		}
	}
	switch in.Action {
	case "create":
		if def.Name == "" || def.BaseURL == "" || def.CredentialID == "" {
			return nil, fmt.Errorf("%w: create needs name, baseUrl and credentialId (a credential from inspect-platform area credentials)", ErrInvalidInput)
		}
		for _, x := range cur {
			if x.Name == def.Name {
				return nil, fmt.Errorf("%w: a Rapid7 source named %q already exists", ErrInvalidInput, def.Name)
			}
		}
		return &networkPlan{target: fmt.Sprintf("create Rapid7 source %q on network %s", def.Name, in.NetworkID), action: "create_rapid7_source", after: req, reversible: false,
			undo:   "none through the API: there is no Rapid7 source delete (disable it with update and collectionDisabled)",
			limits: []string{"this does not test that Rapid7 accepts the credential"},
			do: func(ctx context.Context) error {
				_, _, err := s.Client.Integrations.CreateRapid7(ctx, in.NetworkID, req)
				return err
			},
			verify: shown(def.Name)}, nil
	case "update":
		for _, x := range cur {
			if x.Name != in.Name {
				continue
			}
			if def.Name == "" {
				req.Name = x.Name
				def.Name = x.Name
			}
			if def.BaseURL == "" || def.CredentialID == "" {
				return nil, fmt.Errorf("%w: update REPLACES the source: restate baseUrl and credentialId", ErrInvalidInput)
			}
			before, _ := fwd.Generic(x)
			return &networkPlan{target: fmt.Sprintf("update Rapid7 source %q on network %s", x.Name, in.NetworkID), action: "update_rapid7_source", before: before, after: req, reversible: true,
				undo: "update again with the before values",
				do: func(ctx context.Context) error {
					_, _, err := s.Client.Integrations.UpdateRapid7(ctx, in.NetworkID, in.Name, req)
					return err
				},
				verify: shown(req.Name)}, nil
		}
		return nil, nil
	}
	return nil, fmt.Errorf("%w: object rapid7_source takes action create or update (name is the source to update)", ErrInvalidInput)
}

// planControllerSetup manages a controller-managed setup: controllers (devices Forward logs in to) and the managed devices they report. The controllers' logins are existing credential
// ids, so no secret is read. Deleting one removes the setup and the collection of the devices it manages.
func planControllerSetup(ctx context.Context, s *fwd.Session, in editSourceInput) (*networkPlan, error) {
	if err := noSecretInputs(in); err != nil {
		return nil, err
	}
	cur, _, err := s.Client.ControllerManagedSetups.List(ctx, in.NetworkID)
	if err != nil {
		return nil, err
	}
	find := func(name string) *forward.ControllerManagedSetup {
		for i := range cur {
			if strings.EqualFold(cur[i].Name, name) {
				return &cur[i]
			}
		}
		return nil
	}
	present := func(name string, want bool) func(context.Context) (bool, any, error) {
		return func(ctx context.Context) (bool, any, error) {
			l, _, e := s.Client.ControllerManagedSetups.List(ctx, in.NetworkID)
			for _, x := range l {
				if strings.EqualFold(x.Name, name) {
					return want, nil, e
				}
			}
			return !want, nil, e
		}
	}
	switch in.Action {
	case "create":
		var def forward.NewControllerManagedSetup
		if err := decodeDefinition(in.Definition, &def, "name, controllers[{name, type, host, cliCredentialId, snmpCredentialId, jumpServerId}], managedDevices[{name, type, host, cliCredentialId, snmpCredentialId, jumpServerId, collect}]"); err != nil {
			return nil, err
		}
		if strings.TrimSpace(def.Name) == "" || len(def.Controllers) == 0 {
			return nil, fmt.Errorf("%w: create needs name and at least one controller", ErrInvalidInput)
		}
		if find(def.Name) != nil {
			return nil, fmt.Errorf("%w: a controller-managed setup named %q already exists", ErrInvalidInput, def.Name)
		}
		return &networkPlan{target: fmt.Sprintf("create controller-managed setup %q on network %s", def.Name, in.NetworkID), action: "create_controller_setup", after: def, reversible: true, undo: "delete the setup (action delete)",
			limits: []string{"this does not test that the controllers accept the credentials: run a collection and read inspect-collection"},
			do: func(ctx context.Context) error {
				_, _, err := s.Client.ControllerManagedSetups.Create(ctx, in.NetworkID, def)
				return err
			},
			verify: present(def.Name, true)}, nil
	case "update":
		c := find(in.Name)
		if c == nil {
			return nil, nil
		}
		var def struct {
			ManagedDevices []forward.ManagedDevice `json:"managedDevices"`
		}
		if err := decodeDefinition(in.Definition, &def, "managedDevices (REPLACES the managed device list)"); err != nil {
			return nil, err
		}
		if def.ManagedDevices == nil {
			return nil, fmt.Errorf("%w: update sets managedDevices (the whole list)", ErrInvalidInput)
		}
		before, _ := fwd.Generic(c)
		return &networkPlan{target: "replace the managed devices of controller setup " + c.Name, action: "update_controller_setup", before: before, after: def, reversible: true, undo: "update again with the before managed devices",
			limits: []string{"the list REPLACES the managed devices: restate every device you keep"},
			do: func(ctx context.Context) error {
				_, _, err := s.Client.ControllerManagedSetups.Patch(ctx, in.NetworkID, c.Name, forward.ControllerManagedSetupPatch{ManagedDevices: &def.ManagedDevices})
				return err
			}}, nil
	case "delete":
		c := find(in.Name)
		if c == nil {
			return nil, nil
		}
		before, _ := fwd.Generic(c)
		return &networkPlan{target: "delete controller-managed setup " + c.Name, action: "delete_controller_setup", before: before, reversible: false, confirm: c.Name,
			undo: "none: create it again from the before values", limits: []string{"the devices it manages are no longer collected through it"},
			do: func(ctx context.Context) error {
				_, err := s.Client.ControllerManagedSetups.Delete(ctx, in.NetworkID, c.Name)
				return err
			},
			verify: present(c.Name, false)}, nil
	}
	return nil, fmt.Errorf("%w: controller_setup takes action create, update or delete", ErrInvalidInput)
}

// planMistSetup manages a Juniper Mist cloud-managed setup. apiKeyId names an existing credential, so no secret is read here.
func planMistSetup(ctx context.Context, s *fwd.Session, in editSourceInput) (*networkPlan, error) {
	if err := noSecretInputs(in); err != nil {
		return nil, err
	}
	cur, _, err := s.Client.CloudManagedSetups.ListMist(ctx, in.NetworkID)
	if err != nil {
		return nil, err
	}
	find := func(name string) *forward.MistSetup {
		for i := range cur {
			if cur[i].Name == name {
				return &cur[i]
			}
		}
		return nil
	}
	present := func(name string, want bool) func(context.Context) (bool, any, error) {
		return func(ctx context.Context) (bool, any, error) {
			l, _, e := s.Client.CloudManagedSetups.ListMist(ctx, in.NetworkID)
			for _, x := range l {
				if x.Name == name {
					return want, nil, e
				}
			}
			return !want, nil, e
		}
	}
	switch in.Action {
	case "create":
		var def forward.NewMistSetup
		if err := decodeDefinition(in.Definition, &def, "name, region (GLOBAL_01..05, EMEA_01..04, APAC_01..03), apiKeyId, collect, collectorId, hosts, concurrency"); err != nil {
			return nil, err
		}
		if def.Name == "" || def.Region == "" || def.APIKeyID == "" {
			return nil, fmt.Errorf("%w: create needs name, region and apiKeyId", ErrInvalidInput)
		}
		if find(def.Name) != nil {
			return nil, fmt.Errorf("%w: a Mist setup named %q already exists", ErrInvalidInput, def.Name)
		}
		return &networkPlan{target: fmt.Sprintf("create Mist setup %q on network %s", def.Name, in.NetworkID), action: "create_mist_setup", after: def, reversible: true, undo: "delete the setup (action delete)",
			limits: []string{"this does not test the API key: run a collection and read inspect-collection"},
			do: func(ctx context.Context) error {
				_, _, err := s.Client.CloudManagedSetups.CreateMist(ctx, in.NetworkID, def)
				return err
			},
			verify: present(def.Name, true)}, nil
	case "delete":
		m := find(in.Name)
		if m == nil {
			return nil, nil
		}
		before, _ := fwd.Generic(m)
		return &networkPlan{target: "delete Mist setup " + m.Name, action: "delete_mist_setup", before: before, reversible: false, confirm: m.Name,
			undo: "none: create it again from the before values",
			do: func(ctx context.Context) error {
				_, err := s.Client.CloudManagedSetups.DeleteMist(ctx, in.NetworkID, m.Name)
				return err
			},
			verify: present(m.Name, false)}, nil
	}
	return nil, fmt.Errorf("%w: mist_setup takes action create or delete", ErrInvalidInput)
}
