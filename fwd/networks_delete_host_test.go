//go:build skyforge_host

package fwd

import (
	"context"
	"errors"
	"testing"
)

func TestHostBuildRefusesNetworkDeletionWithoutADeleter(t *testing.T) {
	s := &Session{}
	if err := s.DeleteNetwork(context.Background(), "1"); !errors.Is(err, ErrDeletionRefused) {
		t.Fatalf("a host build with no deleter must refuse: %v", err)
	}
}
