package skills

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/forwardnetworks/fwdctl/fwd"
	"github.com/forwardnetworks/fwdctl/result"
)

const collectionName = "inspect-collection"

func init() { Register(collectionName, inspectCollection) }

// inspectCollection is one door to everything about collection that is read-only: view "status" (how it is going now) or
// "config" (what Forward is asked to collect). Why a finished snapshot is missing devices stays in investigate-collection-failure,
// which classifies; this reports.
func inspectCollection(ctx context.Context, s *fwd.Session, raw json.RawMessage) (result.Result, error) {
	var in struct {
		View string `json:"view"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return result.Result{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	switch in.View {
	case "", "status":
		return inspectCollectionStatus(ctx, s, raw)
	case "config":
		return inspectCollectionConfig(ctx, s, raw)
	}
	return result.Result{}, fmt.Errorf("%w: view must be status or config", ErrInvalidInput)
}
