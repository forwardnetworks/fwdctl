//go:build !skyforge_host

package fwd

import "context"

// deleteNetworkDirect is the CLI's network deletion: the SDK call. A host that must not contain a direct delete builds with -tags skyforge_host (networks_delete_host.go).
func (s *Session) deleteNetworkDirect(ctx context.Context, networkID string) error {
	_, _, err := s.Client.Networks.Delete(ctx, networkID)
	return err
}
