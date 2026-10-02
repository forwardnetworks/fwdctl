//go:build !fwdctl_cli

package fwd

import "context"

// deleteNetworkDirect, in a default build (no -tags fwdctl_cli), contains no direct SDK delete: with no NetworkDeleter set, network deletion is refused. A host that embeds this
// module (and builds it with plain `go build`) therefore never has a direct Networks.Delete in its binary; the fwdctl CLI is built with -tags fwdctl_cli.
func (s *Session) deleteNetworkDirect(context.Context, string) error { return ErrDeletionRefused }
