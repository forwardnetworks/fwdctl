//go:build fwdctl_cli

package fwd

import "context"

// deleteOrganizationDirect is the CLI's organization deletion: the SDK call. It is compiled in ONLY with -tags fwdctl_cli (the fwdctl release builds and scripts/check-delete-seam.sh
// pass it). A plain `go build` of this module, which is what a host that embeds the skills does, gets the refusing stub (organizations_delete_refuse.go).
func (s *Session) deleteOrganizationDirect(ctx context.Context, orgID string) error {
	_, _, err := s.Client.Organizations.Delete(ctx, orgID)
	return err
}
