package skills

import (
	"context"
	"fmt"
	"strings"

	forward "github.com/forwardnetworks/forward-go-sdk"

	"github.com/forwardnetworks/fwdctl/fwd"
)

// orgLicenseRows reads one named organization's stored licenses (Forward support, SaaS only): each license with its tier, dates, whether it has been activated (signed), whether
// the support UI would call it Available, and, for an Available tier license, what activating it is expected to do. The preview applies the rule a field session read from Forward's
// source (nothing is activated or changed here): a stacking tier license needs the org's tier (that of its active, signed, non-trial licenses) to be empty or equal; a replacing one skips that check.
func orgLicenseRows(ctx context.Context, s *fwd.Session, orgID string) ([]any, []string, error) {
	list, _, err := s.Client.Licensing.ListForOrg(ctx, orgID)
	if err != nil {
		return nil, nil, err
	}
	orgTier := ""
	for _, d := range list {
		if d.License != nil && d.Signed && d.Status == "ACTIVE" && d.License.Type != "TIER_TRIAL" && d.License.Tier != "" {
			orgTier = d.License.Tier
			break
		}
	}
	var rows []any
	for _, d := range list {
		row := map[string]any{"id": d.ID, "state": licenseState(d), "status": d.Status, "activated": d.Signed, "invalidated": d.Invalidated, "available": d.Available()}
		if d.UsedAt != "" {
			row["used_at"] = d.UsedAt
		}
		if l := d.License; l != nil {
			row["type"], row["tier"], row["starts_at"], row["expires_at"] = l.Type, l.Tier, l.StartsAt, l.ExpiresAt
			if l.Replaces != nil {
				row["replaces"] = *l.Replaces
			}
			caps := map[string]any{}
			for k, v := range map[string]int{"standard_devices": l.StandardDeviceCap, "lite_devices": l.LiteDeviceCap, "cloud_credit_hours": l.CloudCreditHours, "edge_assets": l.EdgeAssetCap} {
				if v != 0 {
					caps[k] = v
				}
			}
			if len(caps) > 0 {
				row["capacity"] = caps
			}
			if d.Available() {
				row["activation_preview"] = licensePreview(l, orgTier)
			}
		}
		rows = append(rows, row)
	}
	lim := []string{
		"another organization's stored licenses: this needs Forward support authority and a SaaS deployment; the signed key is never returned",
		"state says what the license is to this organization (INVALIDATED, ACTIVATED, AVAILABLE or NOT_ACTIVATED); status is Forward's own date-based status (NOT_ACTIVE_YET, ACTIVE, EXPIRED, REPLACED), so an invalidated license can still read ACTIVE there",
		"available mirrors the support UI's label (not invalidated, not activated, not expired, not a legacy, device, cloud or endpoint license); it is inferred client logic, not a server field",
		"activation_preview applies the tier rule read from Forward's source by a field session and the SDK session, never run: it compares tiers as of now, not as of the license's start date (an NS license that expires before an NSP license starts stops counting), and it does not check cloud-credit debt, a pending replacement or a tier-trial period, which can each still refuse an activation. This skill activates nothing",
	}
	if orgTier != "" {
		lim = append(lim, "the organization's tier here is "+orgTier+" (from its active, activated, non-trial licenses)")
	} else {
		lim = append(lim, "no active activated tier license was found, so no tier check is expected to apply")
	}
	return rows, lim, nil
}

func licensePreview(l *forward.License, orgTier string) string {
	switch {
	case l.Replaces == nil:
		return "not previewed: only tier licenses state whether they replace or stack"
	case *l.Replaces:
		return "replacing: skips the tier check and ends the organization's other active licenses when activated"
	case orgTier == "" || strings.EqualFold(orgTier, l.Tier):
		return "stacking: the tier check is expected to pass"
	default:
		return fmt.Sprintf("stacking: expected to be refused with License tier mismatch: %s and %s (a replacing license, or the %s license ending first, would avoid it)", orgTier, l.Tier, orgTier)
	}
}

// licenseState is the plain state of a stored license: Forward's status is date-based and keeps saying ACTIVE for an invalidated one.
func licenseState(d forward.LicenseDetails) string {
	switch {
	case d.Invalidated:
		return "INVALIDATED"
	case d.Signed:
		return "ACTIVATED"
	case d.Available():
		return "AVAILABLE"
	}
	return "NOT_ACTIVATED"
}
