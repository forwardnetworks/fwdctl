package skills

import (
	"context"
	"fmt"
	"strings"

	forward "github.com/forwardnetworks/forward-go-sdk"

	"github.com/forwardnetworks/fwdctl/fwd"
)

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
