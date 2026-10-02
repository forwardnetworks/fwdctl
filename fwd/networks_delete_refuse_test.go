//go:build !fwdctl_cli

package fwd

import (
	"context"
	"errors"
	"testing"
)

func TestDefaultBuildRefusesNetworkDeletionWithoutADeleter(t *testing.T) {
	s := &Session{}
	if err := s.DeleteNetwork(context.Background(), "1"); !errors.Is(err, ErrDeletionRefused) {
		t.Fatalf("a default build with no deleter must refuse: %v", err)
	}
	called := ""
	s.NetworkDeleter = func(_ context.Context, id string) error { called = id; return nil }
	if err := s.DeleteNetwork(context.Background(), "7"); err != nil || called != "7" {
		t.Fatalf("an injected deleter is used: %v %q", err, called)
	}
}
