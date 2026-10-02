//go:build fwdctl_cli

package fwd

import "context"

// deleteNetworkDirect is the CLI's network deletion: the SDK call. It is compiled in ONLY with -tags fwdctl_cli (the fwdctl release builds and scripts/check-delete-seam.sh pass it).
// A plain `go build` of this module, which is what a host that embeds the skills does, gets the refusing stub (networks_delete_refuse.go) and contains no direct delete.
func (s *Session) deleteNetworkDirect(ctx context.Context, networkID string) error {
	_, _, err := s.Client.Networks.Delete(ctx, networkID)
	return err
}
