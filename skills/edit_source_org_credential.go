package skills

import (
	"context"
	"fmt"
	"strings"

	forward "github.com/forwardnetworks/forward-go-sdk"
	"github.com/forwardnetworks/fwdctl/fwd"
	"github.com/forwardnetworks/fwdctl/result"
)

// planOrgCredential creates or deletes an organization-level HTTP credential (the org's own store, not a network's): the login a Tenable VM scanner uses. It is a LOGIN
// credential whose username is the Tenable access key and whose secret (read from secret_file or secret_env, never the definition) is the secret key.
func planOrgCredential(ctx context.Context, s *fwd.Session, in editSourceInput) (*networkPlan, error) {
	if in.NetworkID != "" {
		return nil, fmt.Errorf("%w: object org_credential is organization-level: give no network_id (a network's credentials are object credential)", ErrInvalidInput)
	}
	cur, _, err := s.Client.Credentials.ListOrgHTTP(ctx)
	if err != nil {
		return nil, err
	}
	switch in.Action {
	case "create":
		var def struct {
			Name          string `json:"name"`
			Username      string `json:"username"`
			Kind          string `json:"kind"`
			LoginType     string `json:"login_type"`
			AutoAssociate *bool  `json:"auto_associate"`
		}
		if err := decodeDefinition(in.Definition, &def, "name, username (the Tenable access key), kind (LOGIN, the default, or API_KEY), login_type, auto_associate"); err != nil {
			return nil, err
		}
		if strings.TrimSpace(def.Name) == "" || strings.TrimSpace(def.Username) == "" {
			return nil, fmt.Errorf("%w: create needs name and username (the Tenable access key for a scanner); the secret key comes from secret_file or secret_env", ErrInvalidInput)
		}
		for _, c := range cur {
			if c.Name == def.Name {
				return nil, fmt.Errorf("%w: an organization credential named %q already exists (id %s)", ErrInvalidInput, c.Name, c.ID)
			}
		}
		req := forward.HTTPCredentialRequest{Type: strings.ToUpper(def.Kind), Name: def.Name, Username: def.Username, LoginType: def.LoginType, AutoAssociate: def.AutoAssociate}
		if in.Apply {
			secs, err := sourceSecrets(in, "password")
			if err != nil {
				return nil, err
			}
			if secs["password"].Reveal() == "" {
				return nil, fmt.Errorf("%w: the secret key is required: give secret_file (mode 600) or secret_env", ErrInvalidInput)
			}
			req.Password = secs["password"].Reveal()
		}
		shown := map[string]any{"name": def.Name, "username": def.Username, "type": firstNonEmpty(req.Type, "LOGIN"), "secret": "from secret_file or secret_env (not shown)"}
		return &networkPlan{target: fmt.Sprintf("create organization HTTP credential %q", def.Name), action: "create_org_credential", after: shown, reversible: true,
			undo:   "delete it (object org_credential action delete, name its id, confirm its id); Forward refuses the delete while a scanner uses it",
			limits: []string{"the credential is stored in the organization, not in a network; the secret is stored by Forward and never read back", "a Tenable VM scanner takes this credential's id as credential_id"},
			do: func(ctx context.Context) error {
				_, _, err := s.Client.Credentials.CreateOrgHTTP(ctx, req)
				return err
			},
			// the id Forward assigned is what a scanner's credential_id takes, so it goes into the change and into the undo
			learn: func(got any, ch *result.Change) {
				if m, ok := got.(map[string]any); ok {
					if id, _ := m["id"].(string); id != "" {
						ch.After = map[string]any{"id": id, "name": def.Name, "username": def.Username, "type": firstNonEmpty(req.Type, "LOGIN"), "secret": "not shown"}
						ch.Undo = fmt.Sprintf("delete it: edit-source object org_credential action delete, name %q, confirm %q (Forward refuses while a scanner uses it)", id, id)
					}
				}
			},
			verify: func(ctx context.Context) (bool, any, error) {
				l, _, e := s.Client.Credentials.ListOrgHTTP(ctx)
				for _, c := range l {
					if c.Name == def.Name {
						return c.Username == def.Username, map[string]any{"id": c.ID, "name": c.Name, "type": c.Type, "username": c.Username}, e
					}
				}
				return false, nil, e
			}}, nil
	case "delete":
		var x *forward.OrgHTTPCredential
		for i := range cur {
			if cur[i].ID == in.Name {
				x = &cur[i]
			}
		}
		if x == nil {
			return nil, nil
		}
		before := map[string]any{"id": x.ID, "name": x.Name, "type": x.Type, "username": x.Username}
		return &networkPlan{target: fmt.Sprintf("delete organization HTTP credential %q (id %s)", x.Name, x.ID), action: "delete_org_credential", before: before, reversible: false, confirm: x.ID,
			undo:   "none: create it again (the secret cannot be read back, so it must be given again)",
			limits: []string{"Forward refuses the delete with a 400 while a scanner uses the credential; the secret is lost"},
			do: func(ctx context.Context) error {
				_, err := s.Client.Credentials.DeleteOrgHTTP(ctx, x.ID)
				return err
			},
			verify: func(ctx context.Context) (bool, any, error) {
				l, _, e := s.Client.Credentials.ListOrgHTTP(ctx)
				for _, c := range l {
					if c.ID == x.ID {
						return false, c.ID, e
					}
				}
				return true, nil, e
			}}, nil
	}
	return nil, fmt.Errorf("%w: object org_credential takes action create or delete (name is the credential id to delete)", ErrInvalidInput)
}
