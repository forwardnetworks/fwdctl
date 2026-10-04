package skills

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
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
	case "collection_settings":
		plan, err = planCollectionSettings(ctx, s, in)
	case "saml":
		plan, err = planSAML(ctx, s, in)
	case "api_tokens":
		plan, err = planAPIToken(ctx, s, in)
	case "licensing":
		plan, err = planLicense(ctx, s, in)
	case "organizations":
		plan, err = planOrganization(ctx, s, in)
	case "cve_index":
		plan, err = planCVEIndex(ctx, s, in)
	default:
		return result.Result{}, fmt.Errorf("%w: area must be banners, webhooks, certificates, access_labels, backups, integrations, collection_settings, saml, api_tokens, licensing, organizations or cve_index", ErrInvalidInput)
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
	case "licensing":
		return in.Action == "apply"
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

// patchShows reports whether every field a patch set reads back the same in the settings Forward now holds (both compared as JSON objects, so no field list has to be kept here).
func patchShows(patch, got any) bool {
	pm, _ := fwd.Generic(patch)
	gm, _ := fwd.Generic(got)
	p, ok1 := pm.(map[string]any)
	g, ok2 := gm.(map[string]any)
	if !ok1 || !ok2 {
		return false
	}
	for k, v := range p {
		if fmt.Sprint(g[k]) != fmt.Sprint(v) {
			return false
		}
	}
	return true
}

// planCollectionSettings changes the organization-wide collection limits (name "organization") or one collector's concurrency (name = the collector id).
func planCollectionSettings(ctx context.Context, s *fwd.Session, in editPlatformInput) (*networkPlan, error) {
	if in.Action != "set" {
		return nil, fmt.Errorf("%w: collection_settings takes action set", ErrInvalidInput)
	}
	if in.Name == "organization" {
		var def struct {
			MaxDeviceAuthNPerSecond     *int `json:"max_device_authn_per_second"`
			MaxScanConnectionsPerSecond *int `json:"max_scan_connections_per_second"`
			DeviceCollectionTimeoutMins *int `json:"device_collection_timeout_minutes"`
			CommandDelayMS              *int `json:"command_delay_ms"`
			PerDeviceConcurrencyBoost   *int `json:"per_device_concurrency_boost"`
		}
		if err := decodeDefinition(in.Definition, &def, "max_device_authn_per_second, max_scan_connections_per_second, device_collection_timeout_minutes, command_delay_ms, per_device_concurrency_boost"); err != nil {
			return nil, err
		}
		patch := forward.OrgCollectionSettingsPatch{MaxDeviceAuthNPerSecond: def.MaxDeviceAuthNPerSecond, MaxScanConnectionsPerSecond: def.MaxScanConnectionsPerSecond,
			DeviceCollectionTimeoutMins: def.DeviceCollectionTimeoutMins, CommandDelayMS: def.CommandDelayMS, PerDeviceConcurrencyBoost: def.PerDeviceConcurrencyBoost}
		if m, _ := fwd.Generic(patch); len(m.(map[string]any)) == 0 {
			return nil, fmt.Errorf("%w: set must change at least one field", ErrInvalidInput)
		}
		cur, _, err := s.Client.Collectors.GetOrganizationSettings(ctx)
		if err != nil {
			return nil, err
		}
		before, _ := fwd.Generic(cur)
		return &networkPlan{target: "change the organization's collection settings", action: "update_org_collection_settings", before: before, after: patch, reversible: true,
			undo:   "set again with the before values",
			limits: []string{"applies to the next collection of every network in the organization; a limit set too low slows or fails collections"},
			do: func(ctx context.Context) error {
				_, err := s.Client.Collectors.PatchOrganizationSettings(ctx, patch)
				return err
			},
			verify: func(ctx context.Context) (bool, any, error) {
				got, _, err := s.Client.Collectors.GetOrganizationSettings(ctx)
				return err == nil && patchShows(patch, got), got, err
			}}, nil
	}
	var def struct {
		Concurrency               *int `json:"concurrency"`
		SNMPCollectionConcurrency *int `json:"snmp_collection_concurrency"`
	}
	if err := decodeDefinition(in.Definition, &def, "concurrency, snmp_collection_concurrency"); err != nil {
		return nil, err
	}
	if def.Concurrency == nil && def.SNMPCollectionConcurrency == nil {
		return nil, fmt.Errorf("%w: set must change concurrency or snmp_collection_concurrency", ErrInvalidInput)
	}
	cols, _, err := s.Client.Collectors.List(ctx)
	if err != nil {
		return nil, err
	}
	var id string
	for _, c := range cols {
		if string(c.ID) == in.Name || c.Name == in.Name {
			id = string(c.ID)
		}
	}
	if id == "" {
		return nil, nil
	}
	cur, _, err := s.Client.Collectors.GetSettings(ctx, id)
	if err != nil {
		return nil, err
	}
	before, _ := fwd.Generic(cur)
	patch := forward.CollectorCollectionSettingsPatch{Concurrency: def.Concurrency, SNMPCollectionConcurrency: def.SNMPCollectionConcurrency}
	return &networkPlan{target: "change collector " + in.Name + " concurrency", action: "update_collector_settings", before: before, after: patch, reversible: true, undo: "set again with the before values",
		do: func(ctx context.Context) error {
			_, err := s.Client.Collectors.PatchSettings(ctx, id, patch)
			return err
		},
		verify: func(ctx context.Context) (bool, any, error) {
			got, _, err := s.Client.Collectors.GetSettings(ctx, id)
			return err == nil && patchShows(patch, got), got, err
		}}, nil
}

// planSAML sets the single sign-on configuration. A wrong value can lock people out of the UI, so applying needs confirm and the limits say how to recover.
func planSAML(ctx context.Context, s *fwd.Session, in editPlatformInput) (*networkPlan, error) {
	if in.Action != "set" {
		return nil, fmt.Errorf("%w: saml takes action set", ErrInvalidInput)
	}
	var def struct {
		CustomName                 string `json:"custom_name"`
		Enabled                    bool   `json:"enabled"`
		Name                       string `json:"name"`
		EntityID                   string `json:"entity_id"`
		SSORedirectURL             string `json:"sso_redirect_url"`
		VerificationCert           string `json:"verification_cert"`
		DisableAuthNRequestSigning bool   `json:"disable_authn_request_signing"`
	}
	if err := decodeDefinition(in.Definition, &def, "custom_name, enabled, name, entity_id, sso_redirect_url, verification_cert (PEM text), disable_authn_request_signing"); err != nil {
		return nil, err
	}
	if def.CustomName == "" {
		return nil, fmt.Errorf("%w: set needs custom_name (the registration id)", ErrInvalidInput)
	}
	if def.Enabled && (def.EntityID == "" || def.SSORedirectURL == "" || !strings.Contains(def.VerificationCert, "BEGIN CERTIFICATE")) {
		return nil, fmt.Errorf("%w: enabled SAML needs entity_id, sso_redirect_url and verification_cert (PEM text)", ErrInvalidInput)
	}
	cur, _, err := s.Client.SAML.GetSettings(ctx)
	if err != nil {
		return nil, err
	}
	var before any
	if cur != nil {
		before, _ = fwd.Generic(cur)
	}
	want := forward.SAMLSettings{CustomName: def.CustomName, SAMLAuthSettings: &forward.SAMLAuthSettings{Enabled: def.Enabled, Name: def.Name, EntityID: def.EntityID,
		SSORedirectURL: def.SSORedirectURL, VerificationCert: def.VerificationCert, DisableAuthNRequestSigning: def.DisableAuthNRequestSigning}}
	shown := map[string]any{"custom_name": def.CustomName, "enabled": def.Enabled, "entity_id": def.EntityID, "sso_redirect_url": def.SSORedirectURL}
	return &networkPlan{target: "set the SAML single sign-on configuration", action: "set_saml", before: before, after: shown, reversible: true, confirm: "saml",
		undo:   "set it again with the before values; if sign-in is broken, an administrator who can still sign in with a password must do it",
		limits: []string{"a wrong entity id, redirect URL or certificate can lock users out of single sign-on; keep an administrator session with a local password open while testing"},
		do:     func(ctx context.Context) error { _, err := s.Client.SAML.PutSettings(ctx, want); return err },
		verify: func(ctx context.Context) (bool, any, error) {
			got, _, err := s.Client.SAML.GetSettings(ctx)
			if err != nil || got == nil || got.SAMLAuthSettings == nil {
				return false, nil, err
			}
			g, _ := fwd.Generic(got)
			return got.CustomName == def.CustomName && got.SAMLAuthSettings.Enabled == def.Enabled && got.SAMLAuthSettings.EntityID == def.EntityID, g, nil
		}}, nil
}

// planAPIToken deletes one of this login's own API tokens. Creating one is not offered: Forward shows the new secret once, and a secret is never returned by these skills.
func planAPIToken(ctx context.Context, s *fwd.Session, in editPlatformInput) (*networkPlan, error) {
	if in.Action != "delete" {
		return nil, fmt.Errorf("%w: api_tokens takes action delete (creating a token would return its secret, which these skills never do; create it in the Forward UI)", ErrInvalidInput)
	}
	toks, _, err := s.Client.Users.ListTokens(ctx)
	if err != nil {
		return nil, err
	}
	for _, t := range toks {
		if t.Name != in.Name {
			continue
		}
		limits := []string{"only this login's own tokens; nothing that used the token can sign in after this"}
		if u := os.Getenv("FORWARD_USERNAME"); u != "" && u == t.AccessKey {
			limits = append(limits, "THIS session signs in with this token: deleting it ends this session's access")
		}
		before, _ := fwd.Generic(t)
		return &networkPlan{target: "delete API token " + t.Name, action: "delete_api_token", before: before, reversible: false, confirm: t.Name,
			undo: "none: a new token has a new secret; create one in the Forward UI and give it to whatever used this one", limits: limits,
			do: func(ctx context.Context) error { _, err := s.Client.Users.DeleteToken(ctx, in.Name); return err },
			verify: func(ctx context.Context) (bool, any, error) {
				ts, _, err := s.Client.Users.ListTokens(ctx)
				for _, x := range ts {
					if x.Name == in.Name {
						return false, x, err
					}
				}
				return true, nil, err
			}}, nil
	}
	return nil, nil
}

// planLicense applies a signed license key. The key is read from the secret, decoded first (a read) so the dry run says what it is, and never returned.
func planLicense(ctx context.Context, s *fwd.Session, in editPlatformInput) (*networkPlan, error) {
	if in.Action != "apply" {
		return nil, fmt.Errorf("%w: licensing takes action apply", ErrInvalidInput)
	}
	if in.SecretFile == "" && in.SecretEnv == "" {
		return nil, fmt.Errorf("%w: apply needs the license key: secret_file or secret_env", ErrInvalidInput)
	}
	sec, err := fwd.ReadSecret(in.SecretFile, in.SecretEnv)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	key := strings.TrimSpace(sec.Reveal())
	dec, _, err := s.Client.Licensing.Decode(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("Forward could not decode the license key, so nothing was planned: %w", err)
	}
	after, _ := fwd.Generic(dec)
	cur, _, _ := s.Client.Licensing.List(ctx)
	before, _ := fwd.Generic(cur)
	return &networkPlan{target: "apply a license key", action: "apply_license", before: before, after: after, reversible: false, confirm: "license",
		undo:   "none through these skills: an applied license cannot be withdrawn here; apply the earlier key again if it is still valid",
		limits: []string{"the key is read from the secret and decoded (a read) even in the dry run; it is never returned", "the licenses listed before are not removed by this"},
		do:     func(ctx context.Context) error { _, _, err := s.Client.Licensing.Apply(ctx, key); return err },
		verify: func(ctx context.Context) (bool, any, error) {
			got, _, err := s.Client.Licensing.List(ctx)
			g, _ := fwd.Generic(got)
			return err == nil && len(got) >= len(cur), g, err
		}}, nil
}

// planOrganization creates, renames, enables or disables, and deletes organizations of a multi-organization deployment (the role needed is Forward's platform administrator).
func planOrganization(ctx context.Context, s *fwd.Session, in editPlatformInput) (*networkPlan, error) {
	orgs, _, err := s.Client.Organizations.List(ctx)
	if err != nil {
		return nil, err
	}
	find := func(key string) *forward.Organization {
		for i := range orgs {
			if string(orgs[i].ID) == key || orgs[i].Name == key {
				return &orgs[i]
			}
		}
		return nil
	}
	state := func(id string, check func(forward.Organization) bool) func(context.Context) (bool, any, error) {
		return func(ctx context.Context) (bool, any, error) {
			os, _, err := s.Client.Organizations.List(ctx)
			for _, o := range os {
				if string(o.ID) == id {
					g, _ := fwd.Generic(o)
					return check(o), g, err
				}
			}
			return check(forward.Organization{}), nil, err
		}
	}
	switch in.Action {
	case "create":
		var def struct {
			Name   string `json:"name"`
			Type   string `json:"type"`
			OnPrem *bool  `json:"on_prem"`
		}
		if err := decodeDefinition(in.Definition, &def, "name, type, on_prem"); err != nil {
			return nil, err
		}
		if strings.TrimSpace(def.Name) == "" {
			return nil, fmt.Errorf("%w: create needs name", ErrInvalidInput)
		}
		if find(def.Name) != nil {
			return nil, fmt.Errorf("%w: an organization named %q already exists", ErrInvalidInput, def.Name)
		}
		return &networkPlan{target: "create organization " + def.Name, action: "create_organization", after: def, reversible: true, undo: "disable it (action disable); deleting needs confirm",
			do: func(ctx context.Context) error {
				_, _, err := s.Client.Organizations.Create(ctx, forward.OrganizationCreateRequest{Name: def.Name, Type: def.Type, OnPrem: def.OnPrem})
				return err
			},
			verify: func(ctx context.Context) (bool, any, error) {
				os, _, err := s.Client.Organizations.List(ctx)
				for _, o := range os {
					if o.Name == def.Name {
						return true, nil, err
					}
				}
				return false, len(os), err
			}}, nil
	}
	cur := find(in.Name)
	if cur == nil {
		return nil, nil
	}
	id := string(cur.ID)
	before, _ := fwd.Generic(cur)
	switch in.Action {
	case "rename":
		var def struct {
			Name string `json:"name"`
		}
		if err := decodeDefinition(in.Definition, &def, "name"); err != nil {
			return nil, err
		}
		if strings.TrimSpace(def.Name) == "" {
			return nil, fmt.Errorf("%w: rename needs the new name", ErrInvalidInput)
		}
		return &networkPlan{target: "rename organization " + cur.Name, action: "rename_organization", before: before, after: def, reversible: true, undo: "rename it back",
			do: func(ctx context.Context) error {
				_, err := s.Client.Organizations.Update(ctx, id, def.Name)
				return err
			},
			verify: state(id, func(o forward.Organization) bool { return o.Name == def.Name })}, nil
	case "enable", "disable":
		off := in.Action == "disable"
		p := &networkPlan{target: in.Action + " organization " + cur.Name, action: in.Action + "_organization", before: before, reversible: true,
			undo:   "run the opposite action",
			limits: []string{"a disabled organization's users cannot sign in; nothing is deleted"},
			do: func(ctx context.Context) error {
				_, err := s.Client.Organizations.SetEnabled(ctx, id, !off)
				return err
			},
			verify: state(id, func(o forward.Organization) bool { return o.Disabled == off })}
		if off {
			p.confirm = cur.Name
		}
		return p, nil
	case "delete":
		return &networkPlan{target: "delete organization " + cur.Name, action: "delete_organization", before: before, reversible: false, confirm: cur.Name,
			undo:   "none: the organization, its networks, snapshots and users are removed",
			limits: []string{"read the organization's networks first; this cannot be recovered through these skills"},
			do:     func(ctx context.Context) error { _, _, err := s.Client.Organizations.Delete(ctx, id); return err },
			verify: func(ctx context.Context) (bool, any, error) {
				os, _, err := s.Client.Organizations.List(ctx)
				for _, o := range os {
					if string(o.ID) == id {
						return false, nil, err
					}
				}
				return true, nil, err
			}}, nil
	}
	return nil, fmt.Errorf("%w: organizations take action create, rename, enable, disable or delete", ErrInvalidInput)
}

// planCVEIndex replaces the vulnerability (CVE) index with a gzip file the caller has (for an installation that cannot reach the vendor feed), or goes back to the index bundled with
// Forward. Forward accepts the change and applies it in the background, so the read-back is not waited for.
func planCVEIndex(ctx context.Context, s *fwd.Session, in editPlatformInput) (*networkPlan, error) {
	cur, _, err := s.Client.CVEIndex.Metadata(ctx)
	if err != nil {
		return nil, err
	}
	before, _ := fwd.Generic(cur)
	switch in.Action {
	case "upload":
		var def struct {
			Path   string `json:"path"`
			SHA256 string `json:"sha256"`
		}
		if err := decodeDefinition(in.Definition, &def, "path (a .gz file), sha256 (optional, checked against the file)"); err != nil {
			return nil, err
		}
		if def.Path == "" {
			return nil, fmt.Errorf("%w: upload needs definition.path, a gzip file of the index", ErrInvalidInput)
		}
		st, serr := os.Stat(def.Path)
		if serr != nil || st.IsDir() {
			return nil, fmt.Errorf("%w: definition.path %s is not a readable file", ErrInvalidInput, def.Path)
		}
		return &networkPlan{target: "replace the CVE index with " + def.Path, action: "upload_cve_index", before: before, after: map[string]any{"path": def.Path, "bytes": st.Size()}, reversible: true,
			undo:   "delete the uploaded index (action delete) to use the bundled one again",
			limits: []string{"Forward accepts the upload and processes it in the background: read inspect-platform area cve_index for the new digest; this skill does not wait", "the file is read on apply, not in the dry run"},
			do: func(ctx context.Context) error {
				b, err := os.ReadFile(def.Path)
				if err != nil {
					return fmt.Errorf("%w: %v", ErrInvalidInput, err)
				}
				_, err = s.Client.CVEIndex.Put(ctx, &forward.CVEIndexDownload{Gzip: b, SHA256: def.SHA256})
				return err
			}}, nil
	case "delete":
		if cur.Bundled() {
			return nil, nil
		}
		return &networkPlan{target: "delete the uploaded CVE index and use the bundled one", action: "delete_cve_index", before: before, reversible: false, confirm: "cve_index",
			undo:   "none: upload the file again",
			limits: []string{"applied in the background; read inspect-platform area cve_index to see the bundled digest"}, do: func(ctx context.Context) error { _, err := s.Client.CVEIndex.Delete(ctx); return err }}, nil
	}
	return nil, fmt.Errorf("%w: cve_index takes action upload or delete", ErrInvalidInput)
}
