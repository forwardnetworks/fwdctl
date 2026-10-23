package skills

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

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
	case "endpoint":
		plan, err = planEndpoint(ctx, s, in)
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
	case "custom_command_group":
		plan, err = planCustomCommandGroup(ctx, s, in)
	case "discovery":
		plan, err = planDiscovery(ctx, s, in)
	default:
		return result.Result{}, fmt.Errorf("%w: object must be credential, jump_server, proxy, classic_device, endpoint, schedule, cloud_account, rapid7_source, controller_setup, mist_setup, custom_command_group or discovery", ErrInvalidInput)
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
		if kind == "CLI" {
			k, kerr := cliCredentialKind(def.Kind, def.Username, def.PrivilegeLevel, def.PrivilegedID)
			if kerr != nil {
				return nil, kerr
			}
			def.Kind = k
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
	case "update":
		return planCredentialUpdate(ctx, s, in, kind, def.Name, def.Username, def.LoginType, def.PrivilegeLevel, def.AutoAssociate, def.PrivilegedID)
	}
	return nil, fmt.Errorf("%w: object credential takes action create, update or delete", ErrInvalidInput)
}

// planCredentialUpdate changes a stored credential in place: the fields given change, the rest stay. A secret (a new password, community string or key) is optional and comes from
// secret_file or secret_env; with none, only the non-secret fields change. name (the input) is the credential id.
func planCredentialUpdate(ctx context.Context, s *fwd.Session, in editSourceInput, kind, newName, username, loginType string, privLevel *int32, autoAssoc *bool, privID string) (*networkPlan, error) {
	if in.Name == "" || (kind != "CLI" && kind != "HTTP" && kind != "SNMP") {
		return nil, fmt.Errorf("%w: update needs name (the credential id, from inspect-platform area credentials) and definition.type CLI, HTTP or SNMP", ErrInvalidInput)
	}
	withSecret := in.SecretFile != "" || in.SecretEnv != ""
	changes := map[string]any{}
	set := func(k string, v any) { changes[k] = v }
	if newName != "" {
		set("name", newName)
	}
	if username != "" {
		set("username", username)
	}
	if loginType != "" {
		set("loginType", loginType)
	}
	if privLevel != nil {
		set("privilegeLevel", *privLevel)
	}
	if autoAssoc != nil {
		set("autoAssociate", *autoAssoc)
	}
	if privID != "" {
		set("privilegedModePasswordId", privID)
	}
	if len(changes) == 0 && !withSecret {
		return nil, fmt.Errorf("%w: update must change a field or rotate the secret (secret_file or secret_env)", ErrInvalidInput)
	}
	// the current credential, by id, so the plan shows what changes
	var before any
	var curName string
	switch kind {
	case "CLI":
		c, _, err := s.Client.Credentials.GetCLI(ctx, in.NetworkID, in.Name)
		if err != nil {
			return nil, err
		}
		if c == nil {
			return nil, nil
		}
		before, _ = fwd.Generic(c)
		curName = c.Name
	case "HTTP":
		c, _, err := s.Client.Credentials.GetHTTP(ctx, in.NetworkID, in.Name)
		if err != nil {
			return nil, err
		}
		if c == nil {
			return nil, nil
		}
		before, _ = fwd.Generic(c)
		curName = c.Name
	case "SNMP":
		l, _, err := s.Client.Credentials.ListSNMP(ctx, in.NetworkID)
		if err != nil {
			return nil, err
		}
		for _, c := range l {
			if string(c.ID) == in.Name {
				before, _ = fwd.Generic(c)
				curName = c.Name
			}
		}
		if before == nil {
			return nil, nil
		}
	}
	after := map[string]any{"fields": changes}
	if withSecret {
		after["secret"] = fwd.SecretFromString("x")
	}
	plan := &networkPlan{target: fmt.Sprintf("update %s credential %s (%s) on network %s", kind, in.Name, curName, in.NetworkID), action: "update_credential", before: before, after: after, reversible: true,
		undo:   "update again with the before fields; a rotated secret cannot be read back, so re-enter the earlier one from its source",
		limits: []string{"the secret, if given, is read from " + secretSource(in) + " only on apply and is never shown, logged or returned; this does not test that the device accepts it"}}
	if in.Apply {
		var secs map[string]fwd.Secret
		if withSecret {
			var err error
			switch kind {
			case "SNMP":
				secs, err = sourceSecrets(in, "communityString", "password", "privacyPassword")
			default:
				secs, err = sourceSecrets(in, "password")
			}
			if err != nil {
				return nil, err
			}
		}
		plan.do = func(ctx context.Context) error {
			str := func(k string) *string {
				if v, ok := changes[k].(string); ok {
					return &v
				}
				return nil
			}
			reveal := func(k string) *string {
				if sec, ok := secs[k]; ok && !sec.Empty() {
					v := sec.Reveal()
					return &v
				}
				return nil
			}
			var err error
			switch kind {
			case "CLI":
				patch := forward.CLICredentialPatch{Name: str("name"), Username: str("username"), Password: reveal("password"), PrivilegedModePasswordID: str("privilegedModePasswordId")}
				if v, ok := changes["privilegeLevel"].(int32); ok {
					patch.PrivilegeLevel = &v
				}
				if v, ok := changes["autoAssociate"].(bool); ok {
					patch.AutoAssociate = &v
				}
				_, _, err = s.Client.Credentials.UpdateCLI(ctx, in.NetworkID, in.Name, patch)
			case "HTTP":
				patch := forward.HTTPCredentialPatch{Name: str("name"), Username: str("username"), Password: reveal("password"), LoginType: str("loginType")}
				if v, ok := changes["autoAssociate"].(bool); ok {
					patch.AutoAssociate = &v
				}
				_, err = s.Client.Credentials.UpdateHTTP(ctx, in.NetworkID, in.Name, patch)
			case "SNMP":
				patch := map[string]any{}
				if v := str("name"); v != nil {
					patch["name"] = *v
				}
				if v := reveal("communityString"); v != nil {
					patch["community"] = *v
				}
				if v := reveal("password"); v != nil {
					patch["authenticationKey"] = *v
				}
				if v := reveal("privacyPassword"); v != nil {
					patch["privacyKey"] = *v
				}
				if v, ok := changes["autoAssociate"].(bool); ok {
					patch["autoAssociate"] = v
				}
				_, err = s.Client.Credentials.UpdateSNMP(ctx, in.NetworkID, in.Name, patch)
			}
			return err
		}
		plan.verify = func(ctx context.Context) (bool, any, error) {
			want := curName
			if n, ok := changes["name"].(string); ok {
				want = n
			}
			user, _ := changes["username"].(string)
			switch kind {
			case "CLI":
				c, _, err := s.Client.Credentials.GetCLI(ctx, in.NetworkID, in.Name)
				if err != nil || c == nil {
					return false, nil, err
				}
				g, _ := fwd.Generic(c)
				return c.Name == want && (user == "" || c.Username == user), g, nil
			case "HTTP":
				c, _, err := s.Client.Credentials.GetHTTP(ctx, in.NetworkID, in.Name)
				if err != nil || c == nil {
					return false, nil, err
				}
				g, _ := fwd.Generic(c)
				return c.Name == want && (user == "" || c.Username == user), g, nil
			}
			return credentialExists(ctx, s, in.NetworkID, kind, want)
		}
	}
	return plan, nil
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
	switch in.Action {
	case "update", "delete":
		return planJumpServerChange(ctx, s, in)
	case "create":
	default:
		return nil, fmt.Errorf("%w: object jump_server takes action create, update or delete", ErrInvalidInput)
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
		after: map[string]any{"host": def.Host, "port": max(def.Port, 22), "username": def.Username, "auth": auth, "secret": "<secret>"}, reversible: true,
		undo:   "delete the new jump server (action delete, name its id, confirm its id)",
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
	if in.Action == "test" {
		return planClassicDeviceTest(ctx, s, in, cur)
	}
	fields := "name, host, type, port, cliCredentialId, cliCredential2Id (the PRIVILEGED_MODE / enable credential), cliCredential3Id (EXPERT_MODE), httpCredentialId, snmpCredentialId, jumpServerId, collectorId, collect, note, enableSnmpCollection, collectBgpAdvertisements, bgpTableType, bgpPeerType, bgpAndBmpFailureHandling, disableIpv6Collection, fullCollectionLog"
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
		var def classicDeviceDef
		if err := decodeDefinition(in.Definition, &def, fields); err != nil {
			return nil, err
		}
		if def.Name == "" || def.Host == "" {
			return nil, fmt.Errorf("%w: create needs name and host", ErrInvalidInput)
		}
		if err := checkDeviceOptions(def.BGPTable, def.BGPPeer, def.BGPFailure); err != nil {
			return nil, err
		}
		if err := bgpNeedsAdvertisements(def.BGPTable, def.BGPPeer, def.CollectBGP); err != nil {
			return nil, err
		}
		if find(def.Name) != nil {
			return nil, fmt.Errorf("%w: a device named %q is already in the list", ErrInvalidInput, def.Name)
		}
		req := forward.ClassicDeviceRequest{Name: def.Name, Host: def.Host, Type: def.Type, Port: def.Port, CLICredentialID: def.CLICredentialID, CLICredential2ID: def.CLICredential2ID, CLICredential3ID: def.CLICredential3ID,
			HTTPCredentialID: def.HTTPCredentialID, SNMPCredentialID: def.SNMPCredentialID, JumpServerID: def.JumpServerID, CollectorID: def.CollectorID, Collect: def.Collect, Note: def.Note,
			EnableSNMPCollection: def.EnableSNMP, CollectBGPAdvertisements: def.CollectBGP, BGPTableType: def.BGPTable, BGPPeerType: def.BGPPeer, BGPAndBMPFailureHandling: def.BGPFailure, DisableIPv6Collection: def.NoIPv6, FullCollectionLog: def.FullLog}
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
		if hasBulkDevices(in.Definition) {
			return planDevicesBulk(ctx, s, in, cur)
		}
		var def classicDeviceDef
		if err := decodeDefinition(in.Definition, &def, fields); err != nil {
			return nil, err
		}
		d := find(in.Name)
		if d == nil {
			return nil, nil
		}
		patch, err := def.patchFor(d)
		if err != nil {
			return nil, err
		}
		rename := def.Name != "" && def.Name != in.Name
		if rename {
			if err := renameCheck(ctx, s, in.NetworkID, in.Name, def.Name, cur); err != nil {
				return nil, err
			}
			nn := def.Name
			patch.Name = &nn
		}
		before, _ := fwd.Generic(d)
		after, _ := fwd.Generic(patch) // only the fields that change: the update is partial, so the rest of the device stays as it is
		if am, ok := after.(map[string]any); ok && len(patch.Clear) > 0 {
			am["clear"] = patch.Clear
		}
		limits := []string{"after lists only the fields this update changes; every other field of the device stays as it is"}
		target, undo := fmt.Sprintf("update device %q on network %s", in.Name, in.NetworkID), "update it again with the before values"
		if rename {
			target = fmt.Sprintf("rename device %q to %q on network %s", in.Name, def.Name, in.NetworkID)
			undo = fmt.Sprintf("rename it back (edit-source classic_device update name %q with definition.name %q)", def.Name, in.Name)
			limits = append(limits, "a rename applies on the next collection (expected behaviour, not verified): collected snapshots keep the old name; tags, layout, link overrides and synthetic-device references follow the rename; the old name answers 404 afterwards and a rename is not idempotent, so do not apply it twice", "the new name must be unique ignoring case across ALL collection source names; this checked classic devices and controller setups, and Forward also refuses a name used by another source type or a synthetic device (400)")
		}
		return &networkPlan{target: target, action: "update_device", before: before, after: after, reversible: true, undo: undo,
			limits: limits,
			do: func(ctx context.Context) error {
				_, _, err := s.Client.ClassicDevices.Patch(ctx, in.NetworkID, in.Name, patch)
				return err
			},
			verify: func(ctx context.Context) (bool, any, error) {
				if !rename {
					return true, nil, nil
				}
				l, _, e := s.Client.ClassicDevices.List(ctx, in.NetworkID)
				for _, x := range l {
					if x.Name == def.Name {
						return true, nil, e
					}
				}
				return false, map[string]any{"renamed_listed": false}, e
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

// cloudDefinitionFields are the definition fields each cloud_account action uses. A field outside its action's list is refused by name: decoding into one shared struct
// would otherwise accept it and the action would ignore it, reporting success for a change that did not happen.
var cloudDefinitionFields = map[string][]string{
	"create": {"type", "name", "collect", "proxyServerId", "regions", "username", "clientId", "clientEmail", "privateKeyId", "tenant", "environment", "subscriptionIds", "concurrency"},
	"rotate": {"type", "name", "username", "clientId", "clientEmail", "privateKeyId", "tenant"},
	"update": {"collect", "accounts_enabled", "c2s_accounts", "accounts_external_ids", "accounts_add", "add_newly_discovered"},
}

// refuseUnusedCloudFields returns an invalid-input error naming any definition field the action does not use.
func refuseUnusedCloudFields(raw json.RawMessage, action string) error {
	if len(raw) == 0 {
		return nil
	}
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(raw, &keys); err != nil {
		return nil // decodeDefinition reports a malformed definition
	}
	allowed := map[string]bool{}
	for _, f := range cloudDefinitionFields[action] {
		allowed[f] = true
	}
	var extra []string
	for k := range keys {
		if !allowed[k] {
			extra = append(extra, k)
		}
	}
	if len(extra) == 0 {
		return nil
	}
	sort.Strings(extra)
	return fmt.Errorf("%w: cloud_account %s does not use definition field(s) %s (it takes: %s); a field it would ignore is refused so a change that did not happen is never reported as done",
		ErrInvalidInput, action, strings.Join(extra, ", "), strings.Join(cloudDefinitionFields[action], ", "))
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
		// AccountsEnabled turns discovered member accounts (AWS assume-role) on or off by account id; update only
		AccountsEnabled map[string]bool `json:"accounts_enabled"`
		// C2SAccounts is a C2S Access Portal (CAP) account's portal account list; update only. A pointer: an empty list is refused (see the plan) but is not the same as absent) is not the same as absent.
		C2SAccounts *[]struct {
			AccountID string `json:"account_id"`
			Role      string `json:"role"`
		} `json:"c2s_accounts"`
	}
	fields := "type (AWS, AZURE, GCP, ...), name, collect, proxyServerId, regions, username (an AWS access key id), clientId, clientEmail, privateKeyId, tenant, environment, subscriptionIds, concurrency, accounts_enabled and c2s_accounts (update only)"
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
		fwd.RedactSecrets(before) // the account as read back carries assume-role external ids
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
	case "update":
		if in.Name == "" {
			return nil, fmt.Errorf("%w: update needs name (the account)", ErrInvalidInput)
		}
		memberEdit := hasMemberEdit(in.Definition)
		if !memberEdit {
			if err := noSecretInputs(in); err != nil {
				return nil, err
			}
		}
		a := find(in.Name)
		if a == nil {
			return nil, nil
		}
		if err := refuseUnusedCloudFields(in.Definition, "update"); err != nil {
			return nil, err
		}
		if memberEdit {
			return planMembers(ctx, s, in, a)
		}
		if err := decodeDefinition(in.Definition, &def, fields); err != nil {
			return nil, err
		}
		isCAP := a.HasCAPCredential()
		if isCAP && len(def.AccountsEnabled) > 0 {
			return nil, fmt.Errorf("%w: %q is collected through a C2S Access Portal credential: Forward filters its accounts with the portal account list, the enabled flags in accounts_enabled are not used for it, so turning an account on or off there would change nothing that is collected. Set the portal account list with c2s_accounts (an empty list collects every account the portal returns), or switch the whole setup with collect", ErrInvalidInput, in.Name)
		}
		if def.C2SAccounts != nil && !isCAP {
			return nil, fmt.Errorf("%w: %q is not collected through a C2S Access Portal credential, so it has no portal account list; use accounts_enabled for its member accounts", ErrInvalidInput, in.Name)
		}
		if def.Collect == nil && len(def.AccountsEnabled) == 0 && def.C2SAccounts == nil {
			return nil, fmt.Errorf("%w: update needs definition.collect (true or false), definition.accounts_enabled (an account id mapped to true or false) or, for a C2S Access Portal setup, definition.c2s_accounts", ErrInvalidInput)
		}
		infos := append([]forward.AWSAssumeRoleInfo(nil), a.AssumeRoleInfos...)
		index := map[string]int{}
		for i, x := range infos {
			index[x.AccountID] = i
		}
		beforeAcc, afterAcc := map[string]bool{}, map[string]bool{}
		for id, on := range def.AccountsEnabled {
			i, ok := index[id]
			if !ok {
				known := make([]string, 0, len(infos))
				for _, x := range infos {
					known = append(known, x.AccountID)
				}
				sort.Strings(known)
				shown, _ := result.CapRow(known, 12)
				return nil, fmt.Errorf("%w: %q is not a member account of %q; its accounts are: %s", ErrInvalidInput, id, in.Name, strings.Join(shown, ", "))
			}
			beforeAcc[id], afterAcc[id] = infos[i].Enabled, on
			infos[i].Enabled = on
		}
		var wantC2S []forward.AWSC2SAccountInfo
		if def.C2SAccounts != nil {
			wantC2S = make([]forward.AWSC2SAccountInfo, 0, len(*def.C2SAccounts))
			for _, x := range *def.C2SAccounts {
				if x.AccountID == "" || x.Role == "" {
					return nil, fmt.Errorf("%w: each c2s_accounts entry needs account_id and role", ErrInvalidInput)
				}
				wantC2S = append(wantC2S, forward.AWSC2SAccountInfo{AccountID: x.AccountID, AccountRole: x.Role})
			}
		}
		if def.C2SAccounts != nil && len(wantC2S) == 0 {
			return nil, fmt.Errorf("%w: c2s_accounts is empty. For a C2S Access Portal setup Forward's collection step requires at least one account and rejects an empty list before it asks the portal, so the AWS snapshot would come back incomplete (read in Forward's source, not run live). The setup test and discovery treat an empty list as no filter, which is why an empty list can look fine in the UI. List the accounts to collect", ErrInvalidInput)
		}
		c2sView := func(l []forward.AWSC2SAccountInfo) []map[string]any {
			out := make([]map[string]any, 0, len(l))
			for _, x := range l {
				out = append(out, map[string]any{"account_id": x.AccountID, "role": x.AccountRole})
			}
			return out
		}
		before := map[string]any{"collect": a.Collect}
		after := map[string]any{"collect": a.Collect}
		if def.Collect != nil {
			after["collect"] = *def.Collect
		}
		if len(def.AccountsEnabled) > 0 {
			before["accounts_enabled"], after["accounts_enabled"] = beforeAcc, afterAcc
		}
		if def.C2SAccounts != nil {
			before["c2s_accounts"], after["c2s_accounts"] = c2sView(a.AWSC2SAccountInfos), c2sView(wantC2S)
		}
		patch := forward.CloudAccountPatch{Type: a.Type, Collect: def.Collect}
		if len(def.AccountsEnabled) > 0 {
			patch.AssumeRoleInfos = &infos
		}
		if def.C2SAccounts != nil {
			patch.AWSC2SAccountInfos = &wantC2S
		}
		limits := []string{"an account's errorMsg is Forward's own record of its last attempt; this does not clear it, and the next collection or a test (action test) refreshes it",
			"this sets what the next collection will use; it does not start one (edit-collection)"}
		if len(def.AccountsEnabled) > 0 {
			limits = append([]string{"the account list is replaced as a whole: every other account is sent unchanged, with its current enabled flag"}, limits...)
		}
		if def.C2SAccounts != nil {
			have := map[[2]string]bool{}
			for _, x := range a.AWSC2SAccountInfos {
				have[[2]string{x.AccountID, x.AccountRole}] = true
			}
			var fresh []string
			for _, w := range wantC2S {
				if !have[[2]string{w.AccountID, w.AccountRole}] {
					fresh = append(fresh, "("+w.AccountID+", "+w.AccountRole+")")
				}
			}
			if len(fresh) > 0 {
				limits = append([]string{"these (account id, role) pairs are not in the account's current list and are not checked against the access portal: " + strings.Join(fresh, ", ") +
					". A wrong account id or role makes the collection filter match nothing, and AWS then yields no data without an error (a thin snapshot, not a failure); confirm them against the accounts the portal offers (the setup's account discovery in the Forward UI) before applying"}, limits...)
			}
			limits = append([]string{"c2s_accounts replaces the whole portal account list: list every account that should be collected (an empty list is refused: Forward's collection step rejects it)"}, limits...)
		}
		return &networkPlan{target: fmt.Sprintf("update cloud account %q on network %s", in.Name, in.NetworkID), action: "update_cloud_account", before: before, after: after, reversible: true,
			undo:   "run update again with the before values (accounts_enabled, c2s_accounts and collect as shown in before)",
			limits: limits,
			do: func(ctx context.Context) error {
				_, _, err := s.Client.CloudAccounts.Patch(ctx, in.NetworkID, in.Name, patch)
				return err
			},
			verify: func(ctx context.Context) (bool, any, error) {
				l, _, e := s.Client.CloudAccounts.List(ctx, in.NetworkID)
				for _, x := range l {
					if x.Name != in.Name {
						continue
					}
					got := map[string]bool{}
					for _, m := range x.AssumeRoleInfos {
						if _, asked := def.AccountsEnabled[m.AccountID]; asked {
							got[m.AccountID] = m.Enabled
						}
					}
					ok := def.Collect == nil || x.Collect == *def.Collect
					for id, on := range def.AccountsEnabled {
						if v, seen := got[id]; !seen || v != on {
							ok = false
						}
					}
					back := map[string]any{"collect": x.Collect}
					if len(def.AccountsEnabled) > 0 {
						back["accounts_enabled"] = got
					}
					if def.C2SAccounts != nil {
						have := map[string]string{}
						for _, c := range x.AWSC2SAccountInfos {
							have[c.AccountID] = c.AccountRole
						}
						if len(have) != len(wantC2S) {
							ok = false
						}
						for _, w := range wantC2S {
							if have[w.AccountID] != w.AccountRole {
								ok = false
							}
						}
						back["c2s_accounts"] = c2sView(x.AWSC2SAccountInfos)
					}
					return ok, back, e
				}
				return false, nil, e
			}}, nil
	case "create", "rotate":
		if err := refuseUnusedCloudFields(in.Definition, in.Action); err != nil {
			return nil, err
		}
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
	return nil, fmt.Errorf("%w: object cloud_account takes action create, update, rotate, test or delete", ErrInvalidInput)
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
	if in.Action != "delete" {
		if err := decodeDefinition(in.Definition, &def, "name, baseUrl, credentialId, disableSslValidation, collectionDisabled, reportNames"); err != nil {
			return nil, err
		}
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
	case "delete":
		for _, x := range cur {
			if x.Name != in.Name {
				continue
			}
			before, _ := fwd.Generic(x)
			return &networkPlan{target: fmt.Sprintf("delete Rapid7 source %q from network %s", x.Name, in.NetworkID), action: "delete_rapid7_source", before: before, reversible: false, confirm: x.Name,
				undo: "none: create it again from the before values (the credential it used is not deleted)",
				do: func(ctx context.Context) error {
					_, err := s.Client.Integrations.DeleteRapid7(ctx, in.NetworkID, x.Name)
					return err
				},
				verify: func(ctx context.Context) (bool, any, error) {
					l, _, e := s.Client.Integrations.ListRapid7(ctx, in.NetworkID)
					for _, y := range l {
						if y.Name == x.Name {
							return false, y, e
						}
					}
					return true, nil, e
				}}, nil
		}
		return nil, nil
	}
	return nil, fmt.Errorf("%w: object rapid7_source takes action create, update or delete (name is the source to update or delete)", ErrInvalidInput)
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
	case "test":
		c := find(in.Name)
		if c == nil {
			return nil, nil
		}
		n := len(c.Controllers) + len(c.ManagedDevices)
		var tdef struct {
			WaitSeconds int `json:"wait_seconds"`
		}
		if len(in.Definition) > 0 {
			if err := decodeDefinition(in.Definition, &tdef, "wait_seconds (wait up to this many seconds, at most 600, for every device to have a result newer than the queue time)"); err != nil {
				return nil, err
			}
		}
		var queuedAt time.Time
		var waitFn func(context.Context) (bool, any, error)
		if tdef.WaitSeconds > 0 {
			waitFn = func(ctx context.Context) (bool, any, error) {
				return waitForSetupTests(ctx, s, in.NetworkID, c.Name, &queuedAt, time.Duration(min(tdef.WaitSeconds, 600))*time.Second)
			}
		}
		return &networkPlan{target: fmt.Sprintf("queue a connectivity test of the %d controller(s) and managed device(s) of setup %s", n, c.Name), action: "test_controller_setup",
			after: map[string]any{"setup": c.Name, "devices_tested": n}, reversible: true, undo: "none needed: a test changes no configuration; it replaces each device's last saved test result",
			limits: []string{"Forward answers as soon as the tests are queued, not when they finish: read each device's result with inspect-collection-config (view config, controller_setups) and compare its tested_at with when you ran this, because the previous result stays until the new one is saved",
				"the route is published by Forward as experimental", "with wait_seconds the apply waits for results newer than the queue time and returns them; without it, read them later with inspect-collection-config"},
			do: func(ctx context.Context) error {
				queuedAt = time.Now()
				_, err := s.Client.ControllerManagedSetups.Test(ctx, in.NetworkID, "", forward.ControllerAndManagedDeviceSelection{SetupNames: []string{c.Name}})
				return err
			},
			verify: waitFn,
			learn: func(got any, ch *result.Change) {
				if rows, ok := got.([]map[string]any); ok {
					ch.After = map[string]any{"setup": c.Name, "devices_tested": n, "results": rows}
				}
			}}, nil
	case "migrate":
		var def forward.NewControllerManagedSetup
		if err := decodeDefinition(in.Definition, &def, "name, controllers[{name, type, host, cliCredentialId, snmpCredentialId, jumpServerId}], managedDevices[{name, type, host, cliCredentialId, snmpCredentialId, jumpServerId, collect}] (list only the devices to move: a subset is a pilot)"); err != nil {
			return nil, err
		}
		if strings.TrimSpace(def.Name) == "" || len(def.Controllers) == 0 {
			return nil, fmt.Errorf("%w: migrate needs name and at least one controller", ErrInvalidInput)
		}
		if find(def.Name) != nil {
			return nil, fmt.Errorf("%w: a controller-managed setup named %q already exists; migrate creates the setup", ErrInvalidInput, def.Name)
		}
		var noLogin []string
		for _, m := range def.ManagedDevices {
			if m.CLICredentialID == "" {
				noLogin = append(noLogin, m.Name)
			}
		}
		if len(noLogin) > 0 {
			return nil, fmt.Errorf("%w: managed device(s) %s have no cliCredentialId: a migrated device inherits nothing from the controller or from the classic device it replaces, so without its own login it cannot be collected", ErrInvalidInput, strings.Join(noLogin, ", "))
		}
		named := map[string]bool{}
		for _, c := range def.Controllers {
			named[strings.ToLower(c.Name)] = true
		}
		for _, m := range def.ManagedDevices {
			named[strings.ToLower(m.Name)] = true
		}
		classic, _, cerr := s.Client.ClassicDevices.List(ctx, in.NetworkID)
		if cerr != nil {
			return nil, cerr
		}
		var gone []map[string]any
		for _, d := range classic {
			if named[strings.ToLower(d.Name)] {
				gone = append(gone, map[string]any{"name": d.Name, "type": d.Type, "host": d.Host, "collector": string(d.CollectorID), "cli_credential": d.CLICredentialID, "cli_credential_2": d.CLICredential2ID, "snmp_credential": d.SNMPCredentialID, "jump_server": d.JumpServerID})
			}
		}
		shown, omitted := result.Cap(gone, 200, "classic devices to delete", "the rest are deleted too; the full list is in the network's classic devices")
		var lim []string
		var pins []string
		for _, g := range gone {
			if c := fmt.Sprint(g["collector"]); c != "" && def.CollectorID == "" {
				pins = append(pins, fmt.Sprintf("%s (%s)", g["name"], c))
			}
		}
		if len(pins) > 0 {
			lim = append(lim, "these classic devices are pinned to a collector: "+strings.Join(pins, ", ")+". A migrated setup has ONE collector for its controllers and guests (collectorId in the definition) and each device's own pin is dropped; with none given the network's default collector is used")
		}
		for _, o := range omitted {
			lim = append(lim, o.Text())
		}
		return &networkPlan{target: fmt.Sprintf("migrate into new controller-managed setup %q on network %s, deleting %d classic device(s)", def.Name, in.NetworkID, len(gone)), action: "migrate_controller_setup",
			before: map[string]any{"classic_devices_to_delete": shown}, after: def, reversible: false, confirm: def.Name,
			undo: "none: this cannot be undone by update. Recreate the classic devices from the before list (edit-source object classic_device action create), and note their device tags and connectivity test results are gone",
			limits: append(lim,
				"Forward deletes, in one transaction with the create, every classic device whose name matches a controller or managed device of this setup (case-insensitive here; Forward may match exactly), with their per-device settings, connectivity test results and device tags; if the create is refused nothing is deleted (read from Forward's source; not exercised on a live Forward)",
				"a migrated device inherits no login from the controller: each managed device carries its own cliCredentialId, which is checked to be present, not to work. Migrate a small pilot (list a few managed devices) and run a connectivity test before moving the rest",
				"the route is published by Forward as experimental"),
			do: func(ctx context.Context) error {
				_, _, err := s.Client.ControllerManagedSetups.CreateMigratingClassic(ctx, in.NetworkID, def)
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
			CollectorID    string                  `json:"collector_id"`
			ClearCollector bool                    `json:"clear_collector"`
		}
		if err := decodeDefinition(in.Definition, &def, "managedDevices (REPLACES the managed device list), collector_id (move the whole setup to that collector), clear_collector (unpin it: the network's default collector applies)"); err != nil {
			return nil, err
		}
		if def.ManagedDevices == nil && def.CollectorID == "" && !def.ClearCollector {
			return nil, fmt.Errorf("%w: update sets managedDevices (the whole list), collector_id or clear_collector", ErrInvalidInput)
		}
		if def.CollectorID != "" && def.ClearCollector {
			return nil, fmt.Errorf("%w: collector_id and clear_collector cannot both be given", ErrInvalidInput)
		}
		patch := forward.ControllerManagedSetupPatch{CollectorID: forward.Identifier(def.CollectorID), ClearCollector: def.ClearCollector}
		var what []string
		if def.ManagedDevices != nil {
			patch.ManagedDevices = &def.ManagedDevices
			what = append(what, "the managed devices")
		}
		if def.CollectorID != "" {
			what = append(what, "its collector to "+def.CollectorID)
		}
		if def.ClearCollector {
			what = append(what, "its collector (back to the network's default)")
		}
		before, _ := fwd.Generic(c)
		return &networkPlan{target: "change " + strings.Join(what, " and ") + " of controller setup " + c.Name, action: "update_controller_setup", before: before, after: patch, reversible: true, undo: "update again with the before values",
			limits: []string{"managedDevices REPLACES the list: restate every device you keep", "a setup has ONE collector for all its controllers and guests; Forward checks only that the collector belongs to the organization, not that it is assigned to this network, and a collector that does not exist answers 404 that reads like a missing setup"},
			do: func(ctx context.Context) error {
				_, _, err := s.Client.ControllerManagedSetups.Patch(ctx, in.NetworkID, c.Name, patch)
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
	return nil, fmt.Errorf("%w: controller_setup takes action create, migrate, test, update or delete", ErrInvalidInput)
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

// planJumpServerChange updates or deletes a jump server by id (name). A change makes Forward re-handle the devices that go through it. A new password, key or certificate is optional and
// comes from the secret: password for a password server, sshKey for a key server (a JSON secret file may carry both sshKey and sshCert).
func planJumpServerChange(ctx context.Context, s *fwd.Session, in editSourceInput) (*networkPlan, error) {
	if in.Name == "" {
		return nil, fmt.Errorf("%w: %s needs name (the jump server id, from inspect-platform area jump_servers)", ErrInvalidInput, in.Action)
	}
	cur, _, err := s.Client.JumpServers.List(ctx, in.NetworkID)
	if err != nil {
		return nil, err
	}
	var js *forward.JumpServer
	for i := range cur {
		if string(cur[i].ID) == in.Name {
			js = &cur[i]
		}
	}
	if js == nil {
		return nil, nil
	}
	before, _ := fwd.Generic(js)
	gone := func(ctx context.Context) (bool, any, error) {
		l, _, e := s.Client.JumpServers.List(ctx, in.NetworkID)
		for _, x := range l {
			if string(x.ID) == in.Name {
				return false, x, e
			}
		}
		return true, nil, e
	}
	if in.Action == "delete" {
		if err := noSecretInputs(in); err != nil {
			return nil, err
		}
		return &networkPlan{target: fmt.Sprintf("delete jump server %s (%s@%s) from network %s", in.Name, js.Username, js.Host, in.NetworkID), action: "delete_jump_server", before: before, reversible: false, confirm: in.Name,
			undo:   "none: create it again with its secret",
			limits: []string{"devices that go through this jump server can no longer be collected until they use another"},
			do: func(ctx context.Context) error {
				_, err := s.Client.JumpServers.Delete(ctx, in.NetworkID, in.Name)
				return err
			},
			verify: gone}, nil
	}
	var def struct {
		Host                   *string `json:"host"`
		Port                   *int    `json:"port"`
		Username               *string `json:"username"`
		SupportsPortForwarding *bool   `json:"supportsPortForwarding"`
		VRF                    *string `json:"vrf"`
		AuthTimeoutSeconds     *int    `json:"authenticationTimeoutSeconds"`
		MaxSessions            *int    `json:"maxSessions"`
		MaxStartups            *int    `json:"maxStartups"`
		SecretKind             string  `json:"secret_is"`
	}
	if err := decodeDefinition(in.Definition, &def, "host, port, username, supportsPortForwarding, vrf, authenticationTimeoutSeconds, maxSessions, maxStartups, secret_is (password|sshKey: what the secret is; default password)"); err != nil {
		return nil, err
	}
	withSecret := in.SecretFile != "" || in.SecretEnv != ""
	patch := forward.JumpServerUpdate{Host: def.Host, Port: def.Port, Username: def.Username, SupportsPortForwarding: def.SupportsPortForwarding, VRF: def.VRF,
		AuthenticationTimeoutSeconds: def.AuthTimeoutSeconds, MaxSessions: def.MaxSessions, MaxStartups: def.MaxStartups}
	if m, _ := fwd.Generic(patch); len(m.(map[string]any)) == 0 && !withSecret {
		return nil, fmt.Errorf("%w: update must change a field or rotate the secret (secret_file or secret_env)", ErrInvalidInput)
	}
	kind := firstNonEmpty(def.SecretKind, "password")
	if kind != "password" && kind != "sshKey" {
		return nil, fmt.Errorf("%w: secret_is must be password or sshKey", ErrInvalidInput)
	}
	after := map[string]any{"fields": def}
	if withSecret {
		after["secret"] = fwd.SecretFromString("x")
	}
	plan := &networkPlan{target: fmt.Sprintf("update jump server %s (%s@%s) on network %s", in.Name, js.Username, js.Host, in.NetworkID), action: "update_jump_server", before: before, after: after, reversible: true,
		undo:   "update again with the before fields; a rotated secret cannot be read back, so re-enter the earlier one",
		limits: []string{"a change makes Forward re-handle the devices that go through this jump server", "the secret, if given, is read from " + secretSource(in) + " only on apply and is never shown, logged or returned"}}
	if in.Apply {
		if withSecret {
			secs, err := sourceSecrets(in, kind, "sshCert")
			if err != nil {
				return nil, err
			}
			if v := secs[kind]; !v.Empty() {
				r := v.Reveal()
				if kind == "password" {
					patch.Password = &r
				} else {
					patch.SSHKey = &r
				}
			}
			if v := secs["sshCert"]; !v.Empty() {
				r := v.Reveal()
				patch.SSHCert = &r
			}
		}
		plan.do = func(ctx context.Context) error {
			_, err := s.Client.JumpServers.Update(ctx, in.NetworkID, in.Name, patch)
			return err
		}
		plan.verify = func(ctx context.Context) (bool, any, error) {
			l, _, e := s.Client.JumpServers.List(ctx, in.NetworkID)
			for _, x := range l {
				if string(x.ID) == in.Name {
					g, _ := fwd.Generic(x)
					return (def.Host == nil || x.Host == *def.Host) && (def.Port == nil || x.Port == *def.Port) && (def.Username == nil || x.Username == *def.Username), g, e
				}
			}
			return false, nil, e
		}
	}
	return plan, nil
}

// cliCredentialKind checks a CLI credential's kind and the fields that belong to one kind only (Forward's NewCliCredential): LOGIN and SHELL need a username; privilegeLevel is for PRIVILEGED_MODE;
// privilegedModePasswordId (the stored enable password a login uses) is for LOGIN. It returns the kind in Forward's spelling; empty stays empty, which Forward reads as LOGIN.
func cliCredentialKind(kind, username string, level *int32, privilegedID string) (string, error) {
	k := strings.ToUpper(strings.TrimSpace(kind))
	switch k {
	case "", "LOGIN", "SHELL", "PRIVILEGED_MODE", "EXPERT_MODE":
	default:
		return "", fmt.Errorf("%w: kind %q is not one of LOGIN, PRIVILEGED_MODE, EXPERT_MODE, SHELL (omit it for a login credential)", ErrInvalidInput, kind)
	}
	if (k == "" || k == "LOGIN" || k == "SHELL") && strings.TrimSpace(username) == "" {
		return "", fmt.Errorf("%w: a LOGIN or SHELL credential needs a username", ErrInvalidInput)
	}
	if level != nil && k != "PRIVILEGED_MODE" {
		return "", fmt.Errorf("%w: privilegeLevel is only for kind PRIVILEGED_MODE (the enable level)", ErrInvalidInput)
	}
	if privilegedID != "" && k != "" && k != "LOGIN" {
		return "", fmt.Errorf("%w: privilegedModePasswordId is only for a LOGIN credential (the stored enable password it uses)", ErrInvalidInput)
	}
	return k, nil
}

// checkDeviceOptions checks a device's BGP collection options against Forward's enums (NewClassicDevice), so a typo fails at the dry run, not as a 400.
func checkDeviceOptions(table, peer, failure string) error {
	in := func(field, v string, allowed ...string) error {
		if v == "" {
			return nil
		}
		for _, a := range allowed {
			if v == a {
				return nil
			}
		}
		return fmt.Errorf("%w: %s %q is not one of %s", ErrInvalidInput, field, v, strings.Join(allowed, ", "))
	}
	if err := in("bgpTableType", table, "BOTH", "ADJ_RIB_IN", "ADJ_RIB_OUT"); err != nil {
		return err
	}
	if err := in("bgpPeerType", peer, "BOTH", "EBGP", "IBGP"); err != nil {
		return err
	}
	return in("bgpAndBmpFailureHandling", failure, "FAIL_DEVICE", "SWITCH_TO_CLI", "ALWAYS_USE_CLI", "ALWAYS_USE_BMP", "CONTINUE_WITHOUT_BGP_DATA")
}

// bgpNeedsAdvertisements refuses a BGP table or peer type while advertisement collection is off: Forward honours them only when collectBgpAdvertisements is true and drops them otherwise.
func bgpNeedsAdvertisements(table, peer string, collect *bool) error {
	if (table != "" || peer != "") && (collect == nil || !*collect) {
		return fmt.Errorf("%w: bgpTableType and bgpPeerType take effect only when collectBgpAdvertisements is true (Forward drops them otherwise); set it in the same call", ErrInvalidInput)
	}
	return nil
}

// classicDeviceDef is the definition of a classic device: the fields create takes and update may change. A zero value is "not given".
type classicDeviceDef struct {
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
	CollectorID      string `json:"collectorId"`
	Collect          *bool  `json:"collect"`
	Note             string `json:"note"`
	// collection options
	EnableSNMP *bool  `json:"enableSnmpCollection"`
	CollectBGP *bool  `json:"collectBgpAdvertisements"`
	BGPTable   string `json:"bgpTableType"`
	BGPPeer    string `json:"bgpPeerType"`
	BGPFailure string `json:"bgpAndBmpFailureHandling"`
	NoIPv6     *bool  `json:"disableIpv6Collection"`
	FullLog    *bool  `json:"fullCollectionLog"`
	// session options (SSH and Telnet), BMP and BGP subnets, and SNMP performance thresholds (fractions): update only
	PaginationMode       string   `json:"paginationMode"`
	TerminalWidth        *int32   `json:"terminalWidth"`
	LargeRTT             *bool    `json:"largeRtt"`
	MinBytesReadPerSec   *int64   `json:"minBytesReadPerSec"`
	MaxConnectionsPerMin *int32   `json:"maxConnectionsPerMin"`
	UseFileTransfer      *bool    `json:"useFileTransferForLargeOutput"`
	Prompt               string   `json:"prompt"`
	PromptResponse       string   `json:"promptResponse"`
	BMPStationHost       string   `json:"bmpStationHost"`
	BGPSubnets           []string `json:"bgpSubnetsToCollect"`
	HighCPU              *float32 `json:"highCpuUsage"`
	HighMemory           *float32 `json:"highMemoryUsage"`
	HighInput            *float32 `json:"highInputUtilization"`
	HighOutput           *float32 `json:"highOutputUtilization"`
	MediumCPU            *float32 `json:"mediumCpuUsage"`
	MediumMemory         *float32 `json:"mediumMemoryUsage"`
	MediumInput          *float32 `json:"mediumInputUtilization"`
	MediumOutput         *float32 `json:"mediumOutputUtilization"`
	// Clear resets properties to unset (an explicit null on the wire): the only way to unpin a device from a collector or remove a credential or option.
	Clear []string `json:"clear"`
}

// patchFor builds the partial update this definition asks for, checking the collection options against the device as it is now.
func (def classicDeviceDef) patchFor(cur *forward.ClassicDevice) (forward.ClassicDevicePatch, error) {
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
	set(def.CLICredential2ID, &patch.CLICredential2ID)
	set(def.CLICredential3ID, &patch.CLICredential3ID)
	set(def.SNMPCredentialID, &patch.SNMPCredentialID)
	set(def.JumpServerID, &patch.JumpServerID)
	set(def.HTTPCredentialID, &patch.HTTPCredentialID)
	set(def.Note, &patch.Note)
	set(def.CollectorID, &patch.CollectorID)
	set(def.BGPTable, &patch.BGPTableType)
	set(def.BGPPeer, &patch.BGPPeerType)
	set(def.BGPFailure, &patch.BGPAndBMPFailureHandling)
	patch.EnableSNMPCollection, patch.CollectBGPAdvertisements, patch.DisableIPv6Collection, patch.FullCollectionLog = def.EnableSNMP, def.CollectBGP, def.NoIPv6, def.FullLog
	set(def.PaginationMode, &patch.PaginationMode)
	set(def.Prompt, &patch.Prompt)
	set(def.PromptResponse, &patch.PromptResponse)
	set(def.BMPStationHost, &patch.BMPStationHost)
	patch.TerminalWidth, patch.LargeRTT, patch.MinBytesReadPerSec, patch.MaxConnectionsPerMin, patch.UseFileTransferForLargeOutput = def.TerminalWidth, def.LargeRTT, def.MinBytesReadPerSec, def.MaxConnectionsPerMin, def.UseFileTransfer
	if def.BGPSubnets != nil {
		subnets := append([]string{}, def.BGPSubnets...)
		patch.BGPSubnetsToCollect = &subnets
	}
	patch.HighCPUUsage, patch.HighMemoryUsage, patch.HighInputUtilization, patch.HighOutputUtilization = def.HighCPU, def.HighMemory, def.HighInput, def.HighOutput
	patch.MediumCPUUsage, patch.MediumMemoryUsage, patch.MediumInputUtilization, patch.MediumOutputUtilization = def.MediumCPU, def.MediumMemory, def.MediumInput, def.MediumOutput
	if err := checkSessionAndThresholds(def); err != nil {
		return patch, err
	}
	if err := checkDeviceOptions(def.BGPTable, def.BGPPeer, def.BGPFailure); err != nil {
		return patch, err
	}
	if len(def.Clear) > 0 {
		for _, c := range def.Clear {
			if !deviceClearable[c] {
				return patch, fmt.Errorf("%w: clear %q: only these can be reset to unset: %s", ErrInvalidInput, c, strings.Join(clearableNames(), ", "))
			}
		}
		patch.Clear = append([]string(nil), def.Clear...)
	}
	on := def.CollectBGP
	if on == nil && cur != nil {
		on = cur.CollectBGPAdvertisements
	}
	if err := bgpNeedsAdvertisements(def.BGPTable, def.BGPPeer, on); err != nil {
		return patch, err
	}
	return patch, nil
}

// deviceClearable are the device properties a patch may reset to unset (Forward reads an explicit null as "clear it").
var deviceClearable = map[string]bool{"cliCredentialId": true, "cliCredential2Id": true, "cliCredential3Id": true, "httpCredentialId": true, "snmpCredentialId": true, "jumpServerId": true, "collectorId": true,
	"note": true, "port": true, "enableSnmpCollection": true, "collectBgpAdvertisements": true, "bgpTableType": true, "bgpPeerType": true, "bgpAndBmpFailureHandling": true, "disableIpv6Collection": true, "fullCollectionLog": true,
	"type": true, "collect": true, "paginationMode": true, "terminalWidth": true, "largeRtt": true, "minBytesReadPerSec": true, "maxConnectionsPerMin": true, "useFileTransferForLargeOutput": true, "prompt": true, "promptResponse": true, "bmpStationHost": true, "bgpSubnetsToCollect": true,
	"highCpuUsage": true, "highMemoryUsage": true, "highInputUtilization": true, "highOutputUtilization": true, "mediumCpuUsage": true, "mediumMemoryUsage": true, "mediumInputUtilization": true, "mediumOutputUtilization": true}

func clearableNames() []string {
	out := make([]string, 0, len(deviceClearable))
	for k := range deviceClearable {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// setupTestPollEvery is how often waitForSetupTests re-reads the setup.
var setupTestPollEvery = 3 * time.Second

// waitForSetupTests polls a setup's devices until each has a saved test result that ended after the tests were queued (a result keeps the previous test's time until the new one is saved), or the wait runs out.
// It returns whether every device has a fresh result, with each device's outcome.
func waitForSetupTests(ctx context.Context, s *fwd.Session, networkID, setup string, queuedAt *time.Time, wait time.Duration) (bool, any, error) {
	deadline := time.Now().Add(wait)
	since := queuedAt.Add(-5 * time.Second) // a little slack for the difference between Forward's clock and this one
	for {
		setups, _, err := s.Client.ControllerManagedSetups.ListWithTestResults(ctx, networkID)
		if err != nil {
			return false, nil, err
		}
		var rows []map[string]any
		fresh := true
		found := false
		for _, st := range setups {
			if !strings.EqualFold(st.Name, setup) {
				continue
			}
			found = true
			add := func(name, role string, tr *forward.DeviceTestResult) {
				outcome, at := setupTestOutcome(tr)
				t, perr := time.Parse(time.RFC3339, at)
				isFresh := tr != nil && perr == nil && t.After(since)
				if !isFresh {
					fresh = false
				}
				rows = append(rows, map[string]any{"name": name, "role": role, "test": outcome, "tested_at": at, "newer_than_queue_time": isFresh})
			}
			for _, c := range st.Controllers {
				add(c.Name, "controller", c.TestResult)
			}
			for _, m := range st.ManagedDevices {
				add(m.Name, "managed", m.TestResult)
			}
		}
		if found && fresh {
			return true, rows, nil
		}
		if time.Now().After(deadline) {
			return false, rows, nil
		}
		select {
		case <-ctx.Done():
			return false, rows, ctx.Err()
		case <-time.After(setupTestPollEvery):
		}
	}
}

// maxDeviceTestNames is how many classic devices one test call may name (the SDK's cap on a bulk start).
const maxDeviceTestNames = forward.MaxConnectivityTestDevices

// planClassicDeviceTest queues Forward's connectivity test of a small named set of classic devices. Each test connects to the device's host from its collector (the one it is pinned to, else the network's default),
// so the dry run lists them and says so; apply needs confirm equal to the number of devices. A name that is not a classic device refuses the whole call; there is no whole-network form. With definition wait_seconds the
// apply waits for each device's stored result to be newer than the moment the tests were queued and returns them.
func planClassicDeviceTest(ctx context.Context, s *fwd.Session, in editSourceInput, cur []forward.ClassicDevice) (*networkPlan, error) {
	var def struct {
		Names       []string `json:"names"`
		WaitSeconds int      `json:"wait_seconds"`
	}
	if err := decodeDefinition(in.Definition, &def, "names [the classic devices to test, at most 100], wait_seconds (wait up to this many seconds, at most 600, for fresh results)"); err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var names []string
	for _, n := range def.Names {
		if n = strings.TrimSpace(n); n != "" && !seen[n] {
			seen[n] = true
			names = append(names, n)
		}
	}
	if len(names) == 0 {
		return nil, fmt.Errorf("%w: test takes names, the classic devices to test (a named set, never the whole network)", ErrInvalidInput)
	}
	if len(names) > maxDeviceTestNames {
		return nil, fmt.Errorf("%w: %d devices named; one call tests at most %d: test a smaller set", ErrInvalidInput, len(names), maxDeviceTestNames)
	}
	byName := map[string]forward.ClassicDevice{}
	for _, d := range cur {
		byName[d.Name] = d
	}
	var missing []string
	rows := make([]map[string]any, 0, len(names))
	for _, n := range names {
		d, ok := byName[n]
		if !ok {
			missing = append(missing, n)
			continue
		}
		collector := string(d.CollectorID)
		if collector == "" {
			collector = "the network's default collector"
		}
		rows = append(rows, map[string]any{"name": d.Name, "host": d.Host, "type": d.Type, "collector": collector})
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("%w: not classic devices of this network: %s (Forward would refuse the whole call and queue nothing)", ErrInvalidInput, strings.Join(missing, ", "))
	}
	var queuedAt time.Time
	var waitFn func(context.Context) (bool, any, error)
	before := map[string]string{} // each device's stored error before the retest, to say how many cleared
	if def.WaitSeconds > 0 {
		if sts, serr := s.DeviceTestStatuses(ctx, in.NetworkID); serr == nil {
			for _, st := range sts {
				before[st.Name] = st.ConnectivityError
			}
		}
		wait := time.Duration(min(def.WaitSeconds, 600)) * time.Second
		waitFn = func(ctx context.Context) (bool, any, error) {
			return waitForDeviceTests(ctx, s, in.NetworkID, names, &queuedAt, wait)
		}
	}
	return &networkPlan{target: fmt.Sprintf("queue a connectivity test of %d classic device(s) of network %s: each test connects to the device's host from its collector", len(names), in.NetworkID), action: "test_classic_devices",
		after: map[string]any{"devices": rows, "devices_tested": len(names)}, reversible: true, confirm: strconv.Itoa(len(names)),
		undo: "none needed: a test changes no configuration; it replaces each device's stored test result (a running test can be cancelled with Forward's stop)",
		limits: []string{
			fmt.Sprintf("this probes each named host from its collector (a login attempt per device): %d named, at most %d per call; apply needs confirm equal to %d", len(names), maxDeviceTestNames, len(names)),
			"Forward queues one task per device on the device's collector, which runs them within its own concurrency (shared with every network on that collector and with collections); an offline collector does not refuse, the tests wait",
			"Forward answers as soon as the tests are queued: a stored result keeps its old time until the new test replaces it. With wait_seconds the apply waits for results newer than the queue time; otherwise read them with inspect-collection view connectivity",
			"needs the TEST_COLLECTION_SOURCES permission",
		},
		do: func(ctx context.Context) error {
			resp, err := s.Client.ConnectivityTests.BulkStart(ctx, in.NetworkID, names)
			queuedAt = time.Now()
			if resp != nil && resp.Response != nil {
				if t, perr := http.ParseTime(resp.Header.Get("Date")); perr == nil {
					queuedAt = t // the server's clock, which the stored times use
				}
			}
			return err
		},
		verify: waitFn,
		learn: func(got any, ch *result.Change) {
			if rs, ok := got.([]map[string]any); ok {
				failedBefore, cleared, stillFailing, newlyFailing := 0, 0, 0, 0
				for _, r := range rs {
					was := before[fmt.Sprint(r["name"])]
					now := !strings.HasPrefix(fmt.Sprint(r["test"]), "failed")
					switch {
					case was != "" && now:
						failedBefore++
						cleared++
					case was != "" && !now:
						failedBefore++
						stillFailing++
					case was == "" && !now:
						newlyFailing++
					}
				}
				summary := fmt.Sprintf("%d of %d that failed before now pass, %d still fail, %d newly fail", cleared, failedBefore, stillFailing, newlyFailing)
				ch.After = map[string]any{"devices": rows, "devices_tested": len(names), "results": rs, "summary": summary}
			}
		}}, nil
}

// waitForDeviceTests polls the named classic devices until each has a stored test that ended after the tests were queued, or the wait runs out.
func waitForDeviceTests(ctx context.Context, s *fwd.Session, networkID string, names []string, queuedAt *time.Time, wait time.Duration) (bool, any, error) {
	deadline := time.Now().Add(wait)
	since := queuedAt.Add(-2 * time.Second)
	want := map[string]bool{}
	for _, n := range names {
		want[n] = true
	}
	for {
		sts, err := s.DeviceTestStatuses(ctx, networkID)
		if err != nil {
			return false, nil, err
		}
		var rows []map[string]any
		fresh := true
		seen := 0
		for _, st := range sts {
			if !want[st.Name] {
				continue
			}
			seen++
			at := firstNonEmpty(st.TestEndTime, st.TestSavedAt)
			t, perr := time.Parse(time.RFC3339, at)
			isFresh := perr == nil && t.After(since)
			if !isFresh {
				fresh = false
			}
			outcome := "no error recorded"
			switch {
			case !st.TestResultPresent:
				outcome = "never tested"
			case st.ConnectivityError != "":
				outcome = "failed: " + st.ConnectivityError
				if st.ErrorPhase != "" {
					outcome += " (" + st.ErrorPhase + ")"
				}
			}
			rows = append(rows, map[string]any{"name": st.Name, "test": outcome, "tested_at": at, "newer_than_queue_time": isFresh})
		}
		if seen == len(names) && fresh {
			return true, rows, nil
		}
		if time.Now().After(deadline) {
			return false, rows, nil
		}
		select {
		case <-ctx.Done():
			return false, rows, ctx.Err()
		case <-time.After(setupTestPollEvery):
		}
	}
}

// planCustomCommandGroup manages a network's custom command groups: the CLI commands (or SNMP OIDs) it adds to a device type's collection, which are not Forward built-ins. Create and update are checked with Forward's own
// validation first (a command the device type refuses is reported in the dry run, before anything is sent). To stop one, update it with disabled true; delete removes it and needs confirm.
func planCustomCommandGroup(ctx context.Context, s *fwd.Session, in editSourceInput) (*networkPlan, error) {
	if err := noSecretInputs(in); err != nil {
		return nil, err
	}
	cur, _, err := s.Client.CustomCommandGroups.List(ctx, in.NetworkID)
	if err != nil {
		return nil, err
	}
	find := func(name string) *forward.CustomCommandGroup {
		for i := range cur {
			if cur[i].Name == name {
				return &cur[i]
			}
		}
		return nil
	}
	exists := func(name string, want bool) func(context.Context) (bool, any, error) {
		return func(ctx context.Context) (bool, any, error) {
			l, _, e := s.Client.CustomCommandGroups.List(ctx, in.NetworkID)
			for _, g := range l {
				if g.Name == name {
					return want, nil, e
				}
			}
			return !want, nil, e
		}
	}
	validate := func(types, cmds []string) error {
		if len(cmds) == 0 || len(types) == 0 {
			return nil
		}
		bad, _, verr := s.Client.CustomCommandGroups.Validate(ctx, in.NetworkID, types, cmds)
		if verr != nil {
			return verr
		}
		if len(bad) > 0 {
			var parts []string
			for _, b := range bad {
				parts = append(parts, fmt.Sprintf("%q is refused for %s", b.Command, strings.Join(b.UnsupportedConnTypes, ", ")))
			}
			return fmt.Errorf("%w: Forward's validation refuses: %s", ErrInvalidInput, strings.Join(parts, "; "))
		}
		return nil
	}
	fields := "name, deviceConnTypes, commands (CLI) or oids [{name, oid}] (SNMP), devices (names or globs; empty = every device of the types), disabled"
	switch in.Action {
	case "create":
		var def forward.NewCustomCommandGroup
		if err := decodeDefinition(in.Definition, &def, fields); err != nil {
			return nil, err
		}
		if strings.TrimSpace(def.Name) == "" || len(def.DeviceConnTypes) == 0 || (len(def.Commands) == 0) == (len(def.OIDs) == 0) {
			return nil, fmt.Errorf("%w: create needs name, deviceConnTypes and exactly one of commands and oids", ErrInvalidInput)
		}
		for _, g := range cur {
			if strings.EqualFold(g.Name, def.Name) {
				return nil, fmt.Errorf("%w: a custom command group named %q already exists (names are unique ignoring case)", ErrInvalidInput, g.Name)
			}
		}
		if err := validate(def.DeviceConnTypes, def.Commands); err != nil {
			return nil, err
		}
		return &networkPlan{target: fmt.Sprintf("create custom command group %q on network %s", def.Name, in.NetworkID), action: "create_custom_command_group", after: def, reversible: true,
			undo:   "delete the group (action delete, name it, confirm its name)",
			limits: []string{"the commands were checked with Forward's validation for the device types; a device login that cannot run one will fail its connectivity test as CUSTOM_COMMAND_AUTHORIZATION_FAILED", "this is a custom group of the network, not a Forward built-in"},
			do: func(ctx context.Context) error {
				_, err := s.Client.CustomCommandGroups.Create(ctx, in.NetworkID, def)
				return err
			},
			verify: exists(def.Name, true)}, nil
	case "update":
		g := find(in.Name)
		if g == nil {
			return nil, nil
		}
		var def forward.CustomCommandGroupUpdate
		if err := decodeDefinition(in.Definition, &def, fields+" (partial: only what is given changes; commands or oids replace the whole list)"); err != nil {
			return nil, err
		}
		if def.Name == nil && def.DeviceConnTypes == nil && def.Commands == nil && def.OIDs == nil && def.Devices == nil && def.Disabled == nil {
			return nil, fmt.Errorf("%w: update must change at least one field", ErrInvalidInput)
		}
		types := def.DeviceConnTypes
		if types == nil {
			types = g.DeviceConnTypes
		}
		cmds := def.Commands
		if cmds == nil && def.DeviceConnTypes != nil {
			cmds = g.Commands // changing the types re-checks the stored commands against them
		}
		if err := validate(types, cmds); err != nil {
			return nil, err
		}
		before, _ := fwd.Generic(g)
		return &networkPlan{target: fmt.Sprintf("change custom command group %q on network %s", g.Name, in.NetworkID), action: "update_custom_command_group", before: before, after: def, reversible: true,
			undo:   "update again with the before values (to stop a group without deleting it, set disabled true)",
			limits: []string{"disabled keeps the group but stops collecting it; commands and oids replace the whole list", "this is a custom group of the network, not a Forward built-in"},
			do: func(ctx context.Context) error {
				_, err := s.Client.CustomCommandGroups.Update(ctx, in.NetworkID, g.Name, def)
				return err
			}}, nil
	case "delete":
		g := find(in.Name)
		if g == nil {
			return nil, nil
		}
		before, _ := fwd.Generic(g)
		return &networkPlan{target: fmt.Sprintf("delete custom command group %q from network %s", g.Name, in.NetworkID), action: "delete_custom_command_group", before: before, reversible: true, confirm: g.Name,
			undo:   "create the group again from the before values (action create)",
			limits: []string{"its commands are no longer collected on the next collection; past snapshots keep what they collected. To stop it without removing it, update it with disabled true"},
			do: func(ctx context.Context) error {
				_, err := s.Client.CustomCommandGroups.Delete(ctx, in.NetworkID, g.Name)
				return err
			},
			verify: exists(g.Name, false)}, nil
	}
	return nil, fmt.Errorf("%w: custom_command_group takes action create, update or delete", ErrInvalidInput)
}

// checkSessionAndThresholds refuses what Forward answers 400 to, before any request: a prompt needs its response (and may not be empty or *), the terminal width is 80 to 500, the byte and connection limits are
// positive, a high threshold is 0.75 to 1.0 and a medium one 0.5 to 1.0 and no higher than its high one.
func checkSessionAndThresholds(def classicDeviceDef) error {
	bad := func(format string, a ...any) error {
		return fmt.Errorf("%w: "+format, append([]any{ErrInvalidInput}, a...)...)
	}
	if (def.Prompt != "") != (def.PromptResponse != "") && !slices.Contains(def.Clear, "prompt") && !slices.Contains(def.Clear, "promptResponse") {
		return bad("prompt and promptResponse go together: Forward refuses one without the other")
	}
	if strings.TrimSpace(def.Prompt) == "*" {
		return bad("prompt may not be *")
	}
	if def.TerminalWidth != nil && (*def.TerminalWidth < 80 || *def.TerminalWidth > 500) {
		return bad("terminalWidth is 80 to 500, not %d", *def.TerminalWidth)
	}
	if def.MinBytesReadPerSec != nil && *def.MinBytesReadPerSec <= 0 {
		return bad("minBytesReadPerSec must be greater than 0")
	}
	if def.MaxConnectionsPerMin != nil && *def.MaxConnectionsPerMin <= 0 {
		return bad("maxConnectionsPerMin must be greater than 0")
	}
	if def.PaginationMode != "" && !slices.Contains([]string{"KEEP_UNCHANGED", "DISABLE_PAGINATION", "ENABLE_PAGINATION"}, def.PaginationMode) {
		return bad("paginationMode is KEEP_UNCHANGED, DISABLE_PAGINATION or ENABLE_PAGINATION")
	}
	pairs := []struct {
		name         string
		high, medium *float32
	}{{"cpu", def.HighCPU, def.MediumCPU}, {"memory", def.HighMemory, def.MediumMemory}, {"input utilization", def.HighInput, def.MediumInput}, {"output utilization", def.HighOutput, def.MediumOutput}}
	for _, p := range pairs {
		if p.high != nil && (*p.high < 0.75 || *p.high > 1.0) {
			return bad("the high %s threshold is a fraction from 0.75 to 1.0, not %v", p.name, *p.high)
		}
		if p.medium != nil && (*p.medium < 0.5 || *p.medium > 1.0) {
			return bad("the medium %s threshold is a fraction from 0.5 to 1.0, not %v", p.name, *p.medium)
		}
		if p.high != nil && p.medium != nil && *p.medium > *p.high {
			return bad("the medium %s threshold may not exceed the high one", p.name)
		}
	}
	return nil
}

var sourceNameRE = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// renameCheck validates a classic device rename: the new name uses letters, digits, dots, hyphens and underscores, and no other collection source of the network has it (ignoring case). A case-only change is not a rename.
func renameCheck(ctx context.Context, s *fwd.Session, networkID, oldName, newName string, classic []forward.ClassicDevice) error {
	if !sourceNameRE.MatchString(newName) {
		return fmt.Errorf("%w: a source name uses letters, digits, dots, hyphens and underscores, not %q", ErrInvalidInput, newName)
	}
	if strings.EqualFold(oldName, newName) {
		return nil
	}
	for _, d := range classic {
		if strings.EqualFold(d.Name, newName) {
			return fmt.Errorf("%w: the name %q is already used by another classic device (names are unique ignoring case across every collection source)", ErrInvalidInput, newName)
		}
	}
	if setups, _, err := s.Client.ControllerManagedSetups.List(ctx, networkID); err == nil {
		for _, st := range setups {
			names := []string{st.Name}
			for _, c := range st.Controllers {
				names = append(names, c.Name)
			}
			for _, m := range st.ManagedDevices {
				names = append(names, m.Name)
			}
			for _, n := range names {
				if strings.EqualFold(n, newName) {
					return fmt.Errorf("%w: the name %q is already used by a controller setup or one of its devices (names are unique ignoring case across every collection source)", ErrInvalidInput, newName)
				}
			}
		}
	}
	return nil
}
