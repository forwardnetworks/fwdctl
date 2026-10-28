package skills

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	forward "github.com/forwardnetworks/forward-go-sdk"

	"github.com/forwardnetworks/fwdctl/fwd"
)

// licenseActivateDefinition is the definition of edit-platform licensing activate: the stored licence to activate and, for an org that uses Software Central (an on-prem licence), the fingerprint of the customer's own deployment.
type licenseActivateDefinition struct {
	LicenseID   string `json:"license_id"`
	Fingerprint string `json:"fingerprint"`
}

// planLicenseActivate activates one stored licence on one NAMED organization (name = the organization id). SaaS only, platform-scoped, Preview. It cannot be undone one licence at a time (Forward refuses to invalidate an
// activated licence, and a second activation), so apply needs confirm equal to the organization id. The dry run reads the org's licences and refuses what Forward would refuse: an unknown licence, an invalidated one, one already activated.
func planLicenseActivate(ctx context.Context, s *fwd.Session, in editPlatformInput) (*networkPlan, error) {
	org := strings.TrimSpace(in.Name)
	var d licenseActivateDefinition
	if len(in.Definition) > 0 {
		if err := json.Unmarshal(in.Definition, &d); err != nil {
			return nil, fmt.Errorf("%w: definition: %v", ErrInvalidInput, err)
		}
	}
	d.LicenseID, d.Fingerprint = strings.TrimSpace(d.LicenseID), strings.TrimSpace(d.Fingerprint)
	if org == "" || d.LicenseID == "" {
		return nil, fmt.Errorf("%w: licensing activate needs name (the organization id) and definition.license_id; definition.fingerprint only for an on-prem licence (an org with Software Central on)", ErrInvalidInput)
	}
	list, _, err := s.Client.Licensing.ListForOrg(ctx, org)
	if errors.Is(err, forward.ErrEndpointNotServed) {
		return nil, fmt.Errorf("%w: this Forward does not serve per-organization licences (SaaS only); nothing was planned", ErrInvalidInput)
	}
	if err != nil {
		return nil, err
	}
	var found *forward.LicenseDetails
	for i := range list {
		if list[i].ID == d.LicenseID {
			found = &list[i]
		}
	}
	if found == nil {
		return nil, fmt.Errorf("%w: organization %s has no stored licence %s (it holds %d); nothing was planned", ErrInvalidInput, org, d.LicenseID, len(list))
	}
	if found.Invalidated {
		return nil, fmt.Errorf("%w: licence %s was invalidated by support and can never be activated", ErrInvalidInput, d.LicenseID)
	}
	if found.Signed {
		return nil, fmt.Errorf("%w: licence %s is already activated; a second activation is refused by Forward", ErrInvalidInput, d.LicenseID)
	}
	before, _ := fwd.Generic(list)
	form := "SaaS form (no fingerprint)"
	if d.Fingerprint != "" {
		form = "on-prem form (bound to the fingerprint given, for good)"
	}
	return &networkPlan{target: "activate licence " + d.LicenseID + " on organization " + org, action: "activate_license", before: before,
		after:      map[string]any{"organization": org, "license_id": d.LicenseID, "form": form, "activated": true},
		reversible: false, confirm: org,
		undo: "none one licence at a time: Forward refuses to invalidate an activated licence and to activate twice; only removing ALL of the organization's licences clears it, and that is not offered here",
		limits: []string{"changes what the organization is entitled to; SaaS only and platform-scoped (a login without the platform support role is refused); Preview route read from Forward's source, not run on a live Forward",
			"whether the org uses Software Central decides which form Forward accepts, and this skill does not read it: a wrong form is refused with a 400 and nothing changes, but a fingerprint from the wrong deployment is accepted and binds the licence to it for good",
			"the licences listed in before include every stored licence of the organization"},
		do: func(ctx context.Context) error {
			_, _, err := s.Client.Licensing.ActivateForOrg(ctx, org, d.LicenseID, d.Fingerprint)
			return err
		},
		verify: func(ctx context.Context) (bool, any, error) {
			got, _, err := s.Client.Licensing.ListForOrg(ctx, org)
			if err != nil {
				return false, nil, err
			}
			g, _ := fwd.Generic(got)
			for _, l := range got {
				if l.ID == d.LicenseID {
					return l.Signed, g, nil
				}
			}
			return false, g, nil
		}}, nil
}
