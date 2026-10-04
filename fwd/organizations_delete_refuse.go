//go:build !fwdctl_cli

package fwd

import "context"

// deleteOrganizationDirect, in a default build (no -tags fwdctl_cli), contains no direct SDK delete: with no OrganizationDeleter set, organization deletion is refused. A host that
// embeds this module (and builds it with plain `go build`) therefore never has a direct Organizations.Delete in its binary; the fwdctl CLI is built with -tags fwdctl_cli.
func (s *Session) deleteOrganizationDirect(context.Context, string) error {
	return ErrOrganizationDeletionRefused
}
