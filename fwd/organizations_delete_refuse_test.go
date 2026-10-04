//go:build !fwdctl_cli

package fwd

import (
	"context"
	"errors"
	"testing"
)

func TestDefaultBuildRefusesOrganizationDeletionWithoutADeleter(t *testing.T) {
	s := &Session{}
	if err := s.DeleteOrganization(context.Background(), "o1"); !errors.Is(err, ErrOrganizationDeletionRefused) {
		t.Fatalf("a default build with no deleter must refuse: %v", err)
	}
	called := ""
	s.OrganizationDeleter = func(_ context.Context, id string) error { called = id; return nil }
	if err := s.DeleteOrganization(context.Background(), "o7"); err != nil || called != "o7" {
		t.Fatalf("an injected deleter is used: %v %q", err, called)
	}
	s.OrganizationDeleter = func(context.Context, string) error { return ErrOrganizationDeletionRefused }
	if err := s.DeleteOrganization(context.Background(), "o7"); !errors.Is(err, ErrOrganizationDeletionRefused) {
		t.Fatalf("a refusing deleter refuses: %v", err)
	}
}
