package fwd

import (
	"context"
	"strings"

	forward "github.com/forwardnetworks/forward-go-sdk"
)

// OrgConfig is what Forward's configuration API says about the login's organization, one read per view of it. Every read is separate and may fail on
// its own (a login without permission for one, an older build); a failed read leaves its map nil and its reason in Errs, so a caller can tell "the
// property is not in the set" from "the set could not be read".
//
// The views are Forward's own (GET /api/config?filter=..., GET /api/global-config):
//   - Effective: every property the build defines, with the value the organization gets (an org override if it has one, else the deployment default).
//   - Configured: the properties the organization holds an explicit row for, even when the row equals the default.
//   - Overridden: the configured ones whose value differs from the deployment default.
//   - NonDefault: the properties whose effective value differs from the value compiled into the build.
//   - Global: the deployment defaults (a global override if one was set, else the compiled default).
//
// What it does not say: who set a value, when, or any per-network or per-user toggle (Forward holds network-level overrides of a few properties
// that no API route returns).
type OrgConfig struct {
	Effective, Configured, Overridden, NonDefault, Global map[string]string
	Errs                                                  map[string]string
}

func propertyStrings(p forward.PropertyValues) map[string]string {
	out := make(map[string]string, len(p))
	for k, v := range p {
		out[strings.ToLower(string(k))] = forward.PropertyValueString(v)
	}
	return out
}

// EffectiveOrgConfig reads only the effective values (one request): the property names are lower case, the values as Forward serialises them. It is what a skill
// that needs one or two properties reads, instead of the five views OrgConfig takes.
func (s *Session) EffectiveOrgConfig(ctx context.Context) (map[string]string, error) {
	p, _, err := s.Client.Properties.Current(ctx, forward.PropertyFilterOff)
	if err != nil {
		return nil, err
	}
	return propertyStrings(p), nil
}

// OrgConfig reads the five views. It never returns an error: each failed read is recorded in Errs.
func (s *Session) OrgConfig(ctx context.Context) OrgConfig {
	c := OrgConfig{Errs: map[string]string{}}
	read := func(name string, dst *map[string]string, get func() (forward.PropertyValues, *forward.Response, error)) {
		p, _, err := get()
		if err != nil {
			c.Errs[name] = err.Error()
			return
		}
		*dst = propertyStrings(p)
	}
	read("effective", &c.Effective, func() (forward.PropertyValues, *forward.Response, error) {
		return s.Client.Properties.Current(ctx, forward.PropertyFilterOff)
	})
	read("configured", &c.Configured, func() (forward.PropertyValues, *forward.Response, error) {
		return s.Client.Properties.Current(ctx, forward.PropertyFilterConfigured)
	})
	read("overridden", &c.Overridden, func() (forward.PropertyValues, *forward.Response, error) {
		return s.Client.Properties.Current(ctx, forward.PropertyFilterOverridden)
	})
	read("nondefault", &c.NonDefault, func() (forward.PropertyValues, *forward.Response, error) {
		return s.Client.Properties.Current(ctx, forward.PropertyFilterNondefault)
	})
	read("global", &c.Global, func() (forward.PropertyValues, *forward.Response, error) {
		return s.Client.Properties.Global(ctx, forward.PropertyFilterOff)
	})
	return c
}

// SetOrgProperty sets an organization override for the login's own organization (PUT /api/config/{property}; needs the organization-settings permission).
func (s *Session) SetOrgProperty(ctx context.Context, name, value string) error {
	_, _, err := s.Client.Properties.SetCurrent(ctx, forward.OrgProperty(strings.ToLower(name)), value)
	return err
}

// ClearOrgProperty removes the organization's override, so the deployment default applies again (DELETE /api/config/{property}).
func (s *Session) ClearOrgProperty(ctx context.Context, name string) error {
	_, err := s.Client.Properties.ClearCurrent(ctx, forward.OrgProperty(strings.ToLower(name)))
	return err
}

// OrgConfigFor reads another organization's configuration through the Forward-support routes (GET /api/orgs/{id}/config) plus the deployment defaults. Effective and Configured are
// required: a login without support permission gets a 403 there and Effective stays nil with the reason in Errs. It never writes and never touches global-config.
func (s *Session) OrgConfigFor(ctx context.Context, orgID string) OrgConfig {
	c := OrgConfig{Errs: map[string]string{}}
	if p, _, err := s.Client.Properties.Organization(ctx, orgID, forward.PropertyFilterOff); err != nil {
		c.Errs["effective"] = err.Error()
		return c
	} else {
		c.Effective = propertyStrings(p)
	}
	if p, _, err := s.Client.Properties.Organization(ctx, orgID, forward.PropertyFilterConfigured); err != nil {
		c.Errs["configured"] = err.Error()
	} else {
		c.Configured = propertyStrings(p)
	}
	if p, _, err := s.Client.Properties.Global(ctx, forward.PropertyFilterOff); err != nil {
		c.Errs["global"] = err.Error()
	} else {
		c.Global = propertyStrings(p)
	}
	return c
}

// OrgName is the name of the organization with this id, from the organization list ("" when the login cannot list it or the id is not there).
func (s *Session) OrgName(ctx context.Context, orgID string) string {
	orgs, _, err := s.Client.Organizations.List(ctx)
	if err != nil {
		return ""
	}
	for _, o := range orgs {
		if string(o.ID) == orgID {
			return o.Name
		}
	}
	return ""
}

// SetOrgPropertyFor sets an override on another organization (PUT /api/orgs/{id}/config/{property}; needs Forward support permission).
func (s *Session) SetOrgPropertyFor(ctx context.Context, orgID, name, value string) error {
	_, _, err := s.Client.Properties.SetOrganization(ctx, orgID, forward.OrgProperty(strings.ToLower(name)), value)
	return err
}

// ClearOrgPropertyFor removes another organization's override (DELETE /api/orgs/{id}/config/{property}).
func (s *Session) ClearOrgPropertyFor(ctx context.Context, orgID, name string) error {
	_, err := s.Client.Properties.ClearOrganization(ctx, orgID, forward.OrgProperty(strings.ToLower(name)))
	return err
}

// EffectiveOrgConfigFor reads another organization's effective values (one request).
func (s *Session) EffectiveOrgConfigFor(ctx context.Context, orgID string) (map[string]string, error) {
	p, _, err := s.Client.Properties.Organization(ctx, orgID, forward.PropertyFilterOff)
	if err != nil {
		return nil, err
	}
	return propertyStrings(p), nil
}
