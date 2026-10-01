//go:build skyforge_host

package fwd

import "context"

// deleteNetworkDirect, in a host build (-tags skyforge_host), contains no direct SDK delete: with no NetworkDeleter set, deletion is refused.
func (s *Session) deleteNetworkDirect(context.Context, string) error { return ErrDeletionRefused }
