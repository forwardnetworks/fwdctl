package fwd

import (
	"context"
	"errors"

	forward "github.com/forwardnetworks/forward-go-sdk"
)

// ErrProfileInUse is Forward's refusal to delete an endpoint profile that an endpoint in any of the organization's networks still uses.
var ErrProfileInUse = forward.ErrEndpointProfileInUse

// EndpointProfiles lists every endpoint profile of the organization (profiles are organization-wide, not per network).
func (s *Session) EndpointProfiles(ctx context.Context) ([]forward.EndpointProfile, error) {
	list, _, err := s.Client.Endpoints.ListProfiles(ctx, "")
	if err == nil {
		s.Annotate(intp(len(list)), false)
	}
	return list, err
}

// CreateEndpointProfile creates a profile from a definition (the read type: only the fields of its type are sent) and returns the stored one.
func (s *Session) CreateEndpointProfile(ctx context.Context, def forward.EndpointProfile) (*forward.EndpointProfile, error) {
	got, _, err := s.Client.Endpoints.CreateProfileDefinition(ctx, def)
	return got, err
}

// AssignEndpointProfile points one endpoint at a profile (its type must match the profile's).
func (s *Session) AssignEndpointProfile(ctx context.Context, networkID, endpoint, endpointType, profileID string) error {
	if profileID == "" {
		return errors.New("a profile id is required")
	}
	_, err := s.Client.Endpoints.Patch(ctx, networkID, endpoint, endpointType, forward.EndpointPatch{ProfileID: &profileID})
	return err
}

// DeleteEndpointProfile removes a profile. Forward refuses (ErrProfileInUse) while any endpoint in the organization uses it; a missing profile is success.
func (s *Session) DeleteEndpointProfile(ctx context.Context, profileID string) error {
	_, err := s.Client.Endpoints.DeleteProfile(ctx, profileID)
	return err
}
