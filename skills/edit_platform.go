package skills

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	forward "github.com/forwardnetworks/forward-go-sdk"

	"github.com/forwardnetworks/fwdctl/fwd"
	"github.com/forwardnetworks/fwdctl/result"
)

const editPlatformName = "edit-platform"

func init() { Register(editPlatformName, editPlatform) }

type editPlatformInput struct {
	Area       string          `json:"area"`
	Action     string          `json:"action"`
	Name       string          `json:"name"`
	Definition json.RawMessage `json:"definition"`
	Confirm    string          `json:"confirm"`
	Apply      bool            `json:"apply"`
	// SecretFile or SecretEnv names where the one secret of the action is (a password, a key); it is read only when applying, and never appears in the input, the result or the log.
	SecretFile string `json:"secret_file"`
	SecretEnv  string `json:"secret_env"`
}

// editPlatform changes what an administrator sets up for the whole organization: banners, webhooks, trusted certificates, device access labels and backups so far. The body is one `definition` object, every
// change is a dry run unless apply is true, and what cannot be put back needs confirm. It touches no device and no network.
func editPlatform(ctx context.Context, s *fwd.Session, raw json.RawMessage) (result.Result, error) {
	var in editPlatformInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return result.Result{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	if bad := secretKeyNamesInDefinition(in.Definition); len(bad) > 0 {
		return result.Result{}, fmt.Errorf("%w: definition has secret-looking field(s) %s: a secret is never put in the input; give secret_file (a path, mode 600) or secret_env (an environment variable name)", ErrInvalidInput, strings.Join(bad, ", "))
	}
	if (in.SecretFile != "" || in.SecretEnv != "") && !platformTakesSecret(in) {
		return result.Result{}, fmt.Errorf("%w: this action takes no secret", ErrInvalidInput)
	}
	cx := result.Context{Scope: "account", State: "current"}
	var plan *networkPlan
	var err error
	switch in.Area {
	case "banners":
		plan, err = planBanner(ctx, s, in)
	case "webhooks":
		plan, err = planWebhook(ctx, s, in)
	case "certificates":
		plan, err = planCertificate(ctx, s, in)
	case "access_labels":
		plan, err = planAccessLabel(ctx, s, in)
	case "backups":
		plan, err = planBackup(ctx, s, in)
	case "integrations":
		plan, err = planIntegration(ctx, s, in)
	default:
		return result.Result{}, fmt.Errorf("%w: area must be banners, webhooks, certificates, access_labels, backups or integrations", ErrInvalidInput)
	}
	if err != nil {
		return result.Result{}, err
	}
	return finishPlan(ctx, editPlatformName, cx, plan, in.Area, in.Action, in.Apply, in.Confirm)
}

func planBanner(ctx context.Context, s *fwd.Session, in editPlatformInput) (*networkPlan, error) {
	var def struct {
		Enabled         *bool    `json:"enabled"`
		Message         *string  `json:"message"`
		BackgroundColor *string  `json:"background_color"`
		NetworkIDs      []string `json:"network_ids"`
	}
	fields := "enabled, message, background_color, network_ids"
	list := func(ctx context.Context) ([]forward.CustomBanner, error) {
		v, _, err := s.Client.Banners.List(ctx)
		return v, err
	}
	switch in.Action {
	case "create":
		if err := decodeDefinition(in.Definition, &def, fields); err != nil {
			return nil, err
		}
		if def.Message == nil || def.BackgroundColor == nil || len(def.NetworkIDs) == 0 {
			return nil, fmt.Errorf("%w: create needs message, background_color and network_ids", ErrInvalidInput)
		}
		req := forward.CustomBannerRequest{Enabled: def.Enabled == nil || *def.Enabled, Message: *def.Message, BackgroundColor: *def.BackgroundColor, NetworkIDs: def.NetworkIDs}
		return &networkPlan{target: "create a banner", action: "create_banner", before: nil, after: req, reversible: true,
			undo:   "update the banner with enabled=false (the API has no banner delete)",
			limits: []string{"a banner cannot be deleted through the API; disabling it hides it"},
			do:     func(ctx context.Context) error { _, _, err := s.Client.Banners.Create(ctx, req); return err },
			verify: func(ctx context.Context) (bool, any, error) {
				bs, err := list(ctx)
				for _, b := range bs {
					if b.Message == req.Message && b.BackgroundColor == req.BackgroundColor {
						return true, nil, nil
					}
				}
				return false, len(bs), err
			}}, nil
	case "update":
		if err := decodeDefinition(in.Definition, &def, fields); err != nil {
			return nil, err
		}
		bs, err := list(ctx)
		if err != nil {
			return nil, err
		}
		for _, cur := range bs {
			if string(cur.ID) != in.Name {
				continue
			}
			next := cur
			if def.Enabled != nil {
				next.Enabled = *def.Enabled
			}
			if def.Message != nil {
				next.Message = *def.Message
			}
			if def.BackgroundColor != nil {
				next.BackgroundColor = *def.BackgroundColor
			}
			if def.NetworkIDs != nil {
				next.NetworkIDs = def.NetworkIDs
			}
			before, _ := fwd.Generic(cur)
			after, _ := fwd.Generic(next)
			return &networkPlan{target: "update banner " + in.Name, action: "update_banner", before: before, after: after, reversible: true,
				undo: "update the banner again with the before values",
				do:   func(ctx context.Context) error { _, err := s.Client.Banners.Replace(ctx, next); return err },
				verify: func(ctx context.Context) (bool, any, error) {
					bs, err := list(ctx)
					for _, b := range bs {
						if b.ID == next.ID {
							return b.Enabled == next.Enabled && b.Message == next.Message && b.BackgroundColor == next.BackgroundColor, b, err
						}
					}
					return false, nil, err
				}}, nil
		}
		return nil, nil
	}
	return nil, fmt.Errorf("%w: banners take action create or update (name is the banner id for update)", ErrInvalidInput)
}

func planWebhook(ctx context.Context, s *fwd.Session, in editPlatformInput) (*networkPlan, error) {
	if in.Action != "create" && strings.TrimSpace(in.Name) == "" {
		return nil, fmt.Errorf("%w: name (the webhook) is required", ErrInvalidInput)
	}
	hooks, _, _, err := s.Client.Webhooks.List(ctx)
	if err != nil {
		return nil, err
	}
	var cur *forward.Webhook
	for i := range hooks {
		if hooks[i].Name == in.Name {
			cur = &hooks[i]
		}
	}
	verifyIs := func(want bool, check func(forward.Webhook) bool) func(context.Context) (bool, any, error) {
		return func(ctx context.Context) (bool, any, error) {
			hs, _, _, err := s.Client.Webhooks.List(ctx)
			for _, h := range hs {
				if h.Name == in.Name {
					return want && check(h), h, err
				}
			}
			return !want, nil, err
		}
	}
	switch in.Action {
	case "create":
		var def struct {
			Name                 string         `json:"name"`
			Description          string         `json:"description"`
			URL                  string         `json:"url"`
			Enabled              *bool          `json:"enabled"`
			DisableSSLValidation bool           `json:"disable_ssl_validation"`
			EventParams          map[string]any `json:"event_params"`
		}
		if err := decodeDefinition(in.Definition, &def, "name, url, description, enabled, disable_ssl_validation, event_params"); err != nil {
			return nil, err
		}
		if def.Name == "" || def.URL == "" {
			return nil, fmt.Errorf("%w: create needs definition name and url", ErrInvalidInput)
		}
		in.Name = def.Name
		for _, h := range hooks {
			if h.Name == def.Name {
				return nil, fmt.Errorf("%w: a webhook named %q already exists; use update", ErrInvalidInput, def.Name)
			}
		}
		req := forward.WebhookRequest{Name: def.Name, Description: def.Description, URL: def.URL, DisableSSLValidation: def.DisableSSLValidation, EventParams: def.EventParams, Enabled: def.Enabled}
		if req.EventParams == nil {
			req.EventParams = map[string]any{}
		}
		return &networkPlan{target: "create webhook " + def.Name, action: "create_webhook", after: req, reversible: true, undo: "delete the webhook",
			limits: []string{"webhook credentials and templates are not set by this skill; webhook schemas are unpublished by Forward (preview)"},
			do:     func(ctx context.Context) error { _, err := s.Client.Webhooks.Create(ctx, req); return err },
			verify: verifyIs(true, func(forward.Webhook) bool { return true })}, nil
	case "update":
		if cur == nil {
			return nil, nil
		}
		var def forward.WebhookPatch
		var raw struct {
			Description *string `json:"description"`
			URL         *string `json:"url"`
			Enabled     *bool   `json:"enabled"`
		}
		if err := decodeDefinition(in.Definition, &raw, "description, url, enabled"); err != nil {
			return nil, err
		}
		if raw.Description == nil && raw.URL == nil && raw.Enabled == nil {
			return nil, fmt.Errorf("%w: update must change description, url or enabled", ErrInvalidInput)
		}
		def = forward.WebhookPatch{Description: raw.Description, URL: raw.URL, Enabled: raw.Enabled}
		before, _ := fwd.Generic(cur)
		return &networkPlan{target: "update webhook " + in.Name, action: "update_webhook", before: before, after: raw, reversible: true, undo: "update again with the before values",
			do: func(ctx context.Context) error { _, err := s.Client.Webhooks.Update(ctx, in.Name, def); return err },
			verify: verifyIs(true, func(h forward.Webhook) bool {
				return (raw.URL == nil || h.URL == *raw.URL) && (raw.Enabled == nil || h.Enabled == *raw.Enabled) && (raw.Description == nil || h.Description == *raw.Description)
			})}, nil
	case "delete":
		if cur == nil {
			return nil, nil
		}
		before, _ := fwd.Generic(cur)
		return &networkPlan{target: "delete webhook " + in.Name, action: "delete_webhook", before: before, reversible: false, confirm: in.Name,
			undo:   "none: create it again from the before values (credentials and templates must be re-entered)",
			do:     func(ctx context.Context) error { _, err := s.Client.Webhooks.Delete(ctx, in.Name); return err },
			verify: verifyIs(false, nil)}, nil
	}
	return nil, fmt.Errorf("%w: webhooks take action create, update or delete", ErrInvalidInput)
}

func planCertificate(ctx context.Context, s *fwd.Session, in editPlatformInput) (*networkPlan, error) {
	certs, _, err := s.Client.TrustedCertificates.List(ctx)
	if err != nil {
		return nil, err
	}
	has := func(name string) *forward.TrustedCertificate {
		for i := range certs {
			if certs[i].Name == name {
				return &certs[i]
			}
		}
		return nil
	}
	present := func(name string, want bool) func(context.Context) (bool, any, error) {
		return func(ctx context.Context) (bool, any, error) {
			cs, _, err := s.Client.TrustedCertificates.List(ctx)
			for _, c := range cs {
				if c.Name == name {
					return want, nil, err
				}
			}
			return !want, nil, err
		}
	}
	switch in.Action {
	case "add":
		var def struct {
			Name        string `json:"name"`
			Certificate string `json:"certificate"`
		}
		if err := decodeDefinition(in.Definition, &def, "name, certificate (PEM text)"); err != nil {
			return nil, err
		}
		if def.Name == "" || !strings.Contains(def.Certificate, "BEGIN CERTIFICATE") {
			return nil, fmt.Errorf("%w: add needs name and a PEM certificate", ErrInvalidInput)
		}
		if has(def.Name) != nil {
			return nil, fmt.Errorf("%w: a trusted certificate named %q already exists", ErrInvalidInput, def.Name)
		}
		return &networkPlan{target: "add trusted certificate " + def.Name, action: "add_trusted_certificate", after: map[string]any{"name": def.Name}, reversible: true, undo: "delete it",
			limits: []string{"collectors do not trust it until action apply pushes the certificates; applying restarts every collector it reaches"},
			do: func(ctx context.Context) error {
				_, _, err := s.Client.TrustedCertificates.Add(ctx, forward.NewTrustedCertificateRequest{Name: def.Name, Certificate: def.Certificate})
				return err
			}, verify: present(def.Name, true)}, nil
	case "delete":
		c := has(in.Name)
		if c == nil {
			return nil, nil
		}
		before, _ := fwd.Generic(c)
		return &networkPlan{target: "delete trusted certificate " + in.Name, action: "delete_trusted_certificate", before: before, reversible: true, undo: "add it again from the before certificate",
			limits: []string{"collectors keep trusting it until action apply pushes the change"},
			do: func(ctx context.Context) error {
				_, err := s.Client.TrustedCertificates.Delete(ctx, in.Name)
				return err
			},
			verify: present(in.Name, false)}, nil
	case "apply":
		return &networkPlan{target: "push the trusted certificates to every collector (each restarts)", action: "apply_trusted_certificates", after: map[string]any{"certificates": len(certs)}, reversible: false, confirm: "apply",
			undo:   "none: a collector restart cannot be undone; push again after changing the certificates",
			limits: []string{"starts collector tasks and returns; it does not wait for them, so completion is not proven"},
			do: func(ctx context.Context) error {
				_, _, err := s.Client.TrustedCertificates.Apply(ctx)
				if forward.IsTrustedCertificateApplyInProgress(err) {
					return nil
				}
				return err
			}}, nil
	}
	return nil, fmt.Errorf("%w: certificates take action add, delete or apply", ErrInvalidInput)
}

func planAccessLabel(ctx context.Context, s *fwd.Session, in editPlatformInput) (*networkPlan, error) {
	labels, _, err := s.Client.AccessControl.ListDeviceAccessLabels(ctx)
	if err != nil {
		return nil, err
	}
	find := func(key string) *forward.DeviceAccessLabel {
		for i := range labels {
			if string(labels[i].ID) == key || labels[i].Name == key {
				return &labels[i]
			}
		}
		return nil
	}
	gone := func(id string) func(context.Context) (bool, any, error) {
		return func(ctx context.Context) (bool, any, error) {
			ls, _, err := s.Client.AccessControl.ListDeviceAccessLabels(ctx)
			for _, l := range ls {
				if string(l.ID) == id {
					return false, l, err
				}
			}
			return true, nil, err
		}
	}
	var def struct {
		Name        *string  `json:"name"`
		DeviceNames []string `json:"device_names"`
		DeviceGlobs []string `json:"device_globs"`
	}
	fields := "name, device_names, device_globs"
	switch in.Action {
	case "create":
		if err := decodeDefinition(in.Definition, &def, fields); err != nil {
			return nil, err
		}
		if def.Name == nil || len(def.DeviceNames)+len(def.DeviceGlobs) == 0 {
			return nil, fmt.Errorf("%w: create needs name and device_names or device_globs", ErrInvalidInput)
		}
		if find(*def.Name) != nil {
			return nil, fmt.Errorf("%w: a device access label named %q already exists", ErrInvalidInput, *def.Name)
		}
		req := forward.DeviceAccessLabelRequest{Name: *def.Name, DeviceNames: def.DeviceNames, DeviceGlobs: def.DeviceGlobs}
		name := *def.Name
		return &networkPlan{target: "create device access label " + name, action: "create_device_access_label", after: req, reversible: true, undo: "delete it",
			limits: []string{"a label restricts nothing until an access group uses it (edit-access)"},
			do: func(ctx context.Context) error {
				_, _, err := s.Client.AccessControl.CreateDeviceAccessLabel(ctx, req)
				return err
			},
			verify: func(ctx context.Context) (bool, any, error) {
				ls, _, err := s.Client.AccessControl.ListDeviceAccessLabels(ctx)
				for _, l := range ls {
					if l.Name == name {
						return true, nil, err
					}
				}
				return false, len(ls), err
			}}, nil
	case "update":
		cur := find(in.Name)
		if cur == nil {
			return nil, nil
		}
		if err := decodeDefinition(in.Definition, &def, fields); err != nil {
			return nil, err
		}
		if def.Name == nil && def.DeviceNames == nil && def.DeviceGlobs == nil {
			return nil, fmt.Errorf("%w: update must change name, device_names or device_globs", ErrInvalidInput)
		}
		req := forward.DeviceAccessLabelRequest{Name: cur.Name, DeviceNames: cur.DeviceNames, DeviceGlobs: cur.DeviceGlobs}
		if def.Name != nil {
			req.Name = *def.Name
		}
		if def.DeviceNames != nil {
			req.DeviceNames = def.DeviceNames
		}
		if def.DeviceGlobs != nil {
			req.DeviceGlobs = def.DeviceGlobs
		}
		before, _ := fwd.Generic(cur)
		id := string(cur.ID)
		return &networkPlan{target: "update device access label " + cur.Name, action: "update_device_access_label", before: before, after: req, reversible: true,
			undo:   "update again with the before names and globs",
			limits: []string{"the devices an access group can reach change for every group that uses this label"},
			do: func(ctx context.Context) error {
				_, _, err := s.Client.AccessControl.UpdateDeviceAccessLabel(ctx, id, req)
				return err
			},
			verify: func(ctx context.Context) (bool, any, error) {
				ls, _, err := s.Client.AccessControl.ListDeviceAccessLabels(ctx)
				for _, l := range ls {
					if string(l.ID) == id {
						return l.Name == req.Name && len(l.DeviceNames) == len(req.DeviceNames) && len(l.DeviceGlobs) == len(req.DeviceGlobs), l, err
					}
				}
				return false, nil, err
			}}, nil
	case "delete":
		cur := find(in.Name)
		if cur == nil {
			return nil, nil
		}
		before, _ := fwd.Generic(cur)
		id := string(cur.ID)
		return &networkPlan{target: "delete device access label " + cur.Name, action: "delete_device_access_label", before: before, reversible: false, confirm: cur.Name,
			undo:   "none: create a label with the before names and globs; it gets a new id and groups must be pointed at it again",
			limits: []string{"groups that used this label lose that restriction; read them with inspect-access first"},
			do: func(ctx context.Context) error {
				_, err := s.Client.AccessControl.DeleteDeviceAccessLabel(ctx, id)
				return err
			},
			verify: gone(id)}, nil
	}
	return nil, fmt.Errorf("%w: access_labels take action create, update or delete (name is the label name or id)", ErrInvalidInput)
}

func planBackup(ctx context.Context, s *fwd.Session, in editPlatformInput) (*networkPlan, error) {
	switch in.Action {
	case "cancel":
		return &networkPlan{target: "cancel the running backup or restore operation", action: "cancel_backup_operation", reversible: false, confirm: "cancel",
			undo: "none: start it again",
			do:   func(ctx context.Context) error { _, err := s.Client.Backups.CancelOperation(ctx); return err }}, nil
	case "delete":
		id, perr := strconv.ParseInt(in.Name, 10, 64)
		if perr != nil || id <= 0 {
			return nil, fmt.Errorf("%w: delete needs name = the numeric backup id (inspect-platform area backups)", ErrInvalidInput)
		}
		list, _, err := s.Client.Backups.ListBackups(ctx)
		if err != nil {
			return nil, err
		}
		for _, b := range list {
			if b.ID != id {
				continue
			}
			before, _ := fwd.Generic(b)
			st := b.StorageType
			if st != forward.StorageTypeInternal && st != forward.StorageTypeS3 {
				st = forward.StorageTypeAll
			}
			return &networkPlan{target: fmt.Sprintf("delete backup %d from %s storage", id, st), action: "delete_backup", before: before, reversible: false, confirm: in.Name,
				undo: "none: a deleted backup cannot be restored",
				do:   func(ctx context.Context) error { _, err := s.Client.Backups.DeleteBackup(ctx, id, st); return err },
				verify: func(ctx context.Context) (bool, any, error) {
					ls, _, err := s.Client.Backups.ListBackups(ctx)
					for _, l := range ls {
						if l.ID == id {
							return false, l, err
						}
					}
					return true, nil, err
				}}, nil
		}
		return nil, nil
	}
	return nil, fmt.Errorf("%w: backups take action cancel or delete (settings, S3 storage and starting a backup need a service principal, which a user login is not)", ErrInvalidInput)
}

// platformTakesSecret says whether the action reads a secret, so a secret given to another action is refused instead of ignored.
func platformTakesSecret(in editPlatformInput) bool {
	switch in.Area {
	case "integrations":
		return (in.Name == "servicenow" && in.Action == "set") || (in.Name == "infoblox" && in.Action == "create")
	}
	return false
}

// planIntegration manages the organization's ServiceNow and Infoblox integrations; name picks which. Rapid7 sources belong to a network and are not here yet.
func planIntegration(ctx context.Context, s *fwd.Session, in editPlatformInput) (*networkPlan, error) {
	needSecret := func() (string, error) {
		sec, err := fwd.ReadSecret(in.SecretFile, in.SecretEnv)
		if err != nil {
			return "", fmt.Errorf("%w: %v", ErrInvalidInput, err)
		}
		return strings.TrimSpace(sec.Reveal()), nil
	}
	if in.Apply && platformTakesSecret(in) {
		if _, err := needSecret(); err != nil {
			return nil, err
		}
	}
	switch in.Name {
	case "servicenow":
		cur, exists, _, err := s.Client.Integrations.GetServiceNow(ctx)
		if err != nil {
			return nil, err
		}
		switch in.Action {
		case "set":
			var def struct {
				InstanceURL       string `json:"instance_url"`
				Username          string `json:"username"`
				Enabled           *bool  `json:"enabled"`
				AutoCreate        bool   `json:"auto_create"`
				AutoCreateImpact  string `json:"auto_create_impact"`
				AutoCreateUrgency string `json:"auto_create_urgency"`
				AutoUpdate        bool   `json:"auto_update"`
			}
			if err := decodeDefinition(in.Definition, &def, "instance_url, username, enabled, auto_create, auto_create_impact, auto_create_urgency, auto_update (the password comes from secret_file or secret_env)"); err != nil {
				return nil, err
			}
			if def.Username == "" || (!exists && def.InstanceURL == "") {
				return nil, fmt.Errorf("%w: set needs username, and instance_url when the integration does not exist yet", ErrInvalidInput)
			}
			if in.SecretFile == "" && in.SecretEnv == "" {
				return nil, fmt.Errorf("%w: set needs the password: secret_file or secret_env", ErrInvalidInput)
			}
			var before any
			if exists {
				before, _ = fwd.Generic(cur)
			}
			return &networkPlan{target: "set the ServiceNow integration", action: "set_servicenow", before: before,
				after: map[string]any{"fields": def, "password": fwd.SecretFromString("x")}, reversible: exists,
				undo:   "set it again with the before values and the earlier password from its source (the password is never read back)",
				limits: []string{"the password cannot be read back, so the read-back checks the other fields only; this does not test that ServiceNow accepts the login"},
				do: func(ctx context.Context) error {
					pw, err := needSecret()
					if err != nil {
						return err
					}
					req := forward.ServiceNowIntegrationRequest{InstanceURL: def.InstanceURL, Username: def.Username, Password: pw, Enabled: def.Enabled == nil || *def.Enabled,
						AutoCreate: def.AutoCreate, AutoCreateImpact: def.AutoCreateImpact, AutoCreateUrgency: def.AutoCreateUrgency, AutoUpdate: def.AutoUpdate}
					_, err = s.Client.Integrations.PatchServiceNow(ctx, req)
					return err
				},
				verify: func(ctx context.Context) (bool, any, error) {
					got, ok, _, err := s.Client.Integrations.GetServiceNow(ctx)
					if err != nil || !ok {
						return false, nil, err
					}
					g, _ := fwd.Generic(got)
					return got.Username == def.Username && (def.InstanceURL == "" || got.InstanceURL == def.InstanceURL), g, nil
				}}, nil
		case "delete":
			if !exists {
				return nil, nil
			}
			before, _ := fwd.Generic(cur)
			return &networkPlan{target: "delete the ServiceNow integration", action: "delete_servicenow", before: before, reversible: false, confirm: "servicenow",
				undo: "none: set it up again (the password must be re-entered)",
				do:   func(ctx context.Context) error { _, err := s.Client.Integrations.DeleteServiceNow(ctx); return err },
				verify: func(ctx context.Context) (bool, any, error) {
					_, ok, _, err := s.Client.Integrations.GetServiceNow(ctx)
					return !ok, nil, err
				}}, nil
		}
		return nil, fmt.Errorf("%w: servicenow takes action set or delete", ErrInvalidInput)
	case "infoblox":
		if in.Action != "create" {
			return nil, fmt.Errorf("%w: infoblox takes action create (the API has no update or delete)", ErrInvalidInput)
		}
		var def struct {
			Name      string `json:"name"`
			IPAddress string `json:"ip_address"`
			Username  string `json:"username"`
		}
		if err := decodeDefinition(in.Definition, &def, "name, ip_address, username (the password comes from secret_file or secret_env)"); err != nil {
			return nil, err
		}
		if def.Name == "" || def.IPAddress == "" || def.Username == "" {
			return nil, fmt.Errorf("%w: create needs name, ip_address and username", ErrInvalidInput)
		}
		if in.SecretFile == "" && in.SecretEnv == "" {
			return nil, fmt.Errorf("%w: create needs the password: secret_file or secret_env", ErrInvalidInput)
		}
		existing, _, err := s.Client.Integrations.ListInfoblox(ctx)
		if err != nil {
			return nil, err
		}
		for _, e := range existing {
			if e.Name == def.Name {
				return nil, fmt.Errorf("%w: an Infoblox instance named %q already exists", ErrInvalidInput, def.Name)
			}
		}
		return &networkPlan{target: "add Infoblox instance " + def.Name, action: "create_infoblox", after: map[string]any{"fields": def, "password": fwd.SecretFromString("x")}, reversible: false,
			undo:   "none through the API: remove it in the Forward UI",
			limits: []string{"the API cannot update or delete an Infoblox instance"},
			do: func(ctx context.Context) error {
				pw, err := needSecret()
				if err != nil {
					return err
				}
				_, _, err = s.Client.Integrations.CreateInfoblox(ctx, forward.InfobloxInstanceRequest{Name: def.Name, IPAddress: def.IPAddress, Username: def.Username, Password: pw})
				return err
			},
			verify: func(ctx context.Context) (bool, any, error) {
				l, _, err := s.Client.Integrations.ListInfoblox(ctx)
				for _, e := range l {
					if e.Name == def.Name {
						return true, nil, err
					}
				}
				return false, len(l), err
			}}, nil
	}
	return nil, fmt.Errorf("%w: integrations name must be servicenow or infoblox", ErrInvalidInput)
}
