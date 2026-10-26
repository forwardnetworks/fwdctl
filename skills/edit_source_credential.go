package skills

import (
	"context"
	"fmt"
	"strings"

	forward "github.com/forwardnetworks/forward-go-sdk"

	"github.com/forwardnetworks/fwdctl/fwd"
)

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
