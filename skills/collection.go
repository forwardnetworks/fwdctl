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
// "config" (what Forward is asked to collect) "migration" (devices Forward's UI says need to move to a controller-managed setup) or "discovery" (Forward's device discovery: jobs, devices found and not yet added, schedules). Why a finished snapshot is missing devices stays in investigate-collection-failure,
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
		var present map[string]json.RawMessage
		_ = json.Unmarshal(raw, &present)
		for _, k := range []string{"collector", "names_only", "device", "data_file", "data_connector", "include_content"} {
			if _, bad := present[k]; bad {
				return result.Result{}, fmt.Errorf("%w: %s is an input of view config (the devices list), not of view status", ErrInvalidInput, k)
			}
		}
		return inspectCollectionStatus(ctx, s, raw)
	case "config":
		return inspectCollectionConfig(ctx, s, raw)
	case "migration":
		return inspectCollectionMigration(ctx, s, raw)
	case "discovery":
		return inspectCollectionDiscovery(ctx, s, raw)
	case "connectivity":
		return inspectCollectionConnectivity(ctx, s, raw)
	}
	return result.Result{}, fmt.Errorf("%w: view must be status, config, migration, discovery or connectivity", ErrInvalidInput)
}
