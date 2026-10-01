package skills

import (
	"context"
	"fmt"
	"sort"

	"github.com/forwardnetworks/fwdctl/fwd"
)

// backdatePlan says what backdating to a snapshot would do: the snapshot and every snapshot created at or after it are invalidated and
// reprocessed, so their answers are unavailable until they finish. It reads only.
type backdatePlan struct {
	Snapshot string   `json:"snapshot_id"`
	Affected []string `json:"snapshots_invalidated"`
	Note     string   `json:"note"`
}

func planBackdate(ctx context.Context, s *fwd.Session, networkID, snapshotID string) (*backdatePlan, error) {
	all, err := s.Snapshots(ctx, networkID)
	if err != nil {
		return nil, err
	}
	var from string
	for _, sn := range all {
		if string(sn.ID) == snapshotID {
			from = snapTime(sn)
		}
	}
	if from == "" {
		return nil, nil
	}
	var hit []string
	for _, sn := range all {
		if snapTime(sn) >= from {
			hit = append(hit, string(sn.ID))
		}
	}
	sort.Strings(hit)
	return &backdatePlan{Snapshot: snapshotID, Affected: hit, Note: fmt.Sprintf("%d snapshot(s) from %s onward would be invalidated and reprocessed; their answers (paths, checks, NQE) are unavailable until they finish", len(hit), snapshotID)}, nil
}
