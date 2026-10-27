package skills

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	forward "github.com/forwardnetworks/forward-go-sdk"

	"github.com/forwardnetworks/fwdctl/fwd"
	"github.com/forwardnetworks/fwdctl/result"
)

// networkMaps reads a network's saved Network Maps (custom diagrams Forward refreshes at every snapshot). Without map_id it lists the maps with their directory; with map_id it reads one map and
// says which of the devices placed on it the snapshot has and which it lacks. Read only. The routes are Preview and need VIEW_NETWORK_MAPS: a refusal comes back as an error the caller words, so an empty
// list from a successful read means no maps are saved, and a failed read means nothing.
func networkMaps(ctx context.Context, s *fwd.Session, in topologyInput, sid string) ([]map[string]any, map[string]any, []string, error) {
	limits := []string{"saved network maps are read through Forward's Preview routes and need the VIEW_NETWORK_MAPS permission; maps belong to the network, and each is redrawn from the snapshot it is shown for"}
	if in.MapID != "" {
		return oneNetworkMap(ctx, s, in, sid, limits)
	}
	maps, _, err := s.Client.NetworkMaps.List(ctx, in.NetworkID)
	if err != nil {
		return nil, nil, nil, err
	}
	dirOf := map[string]string{}
	extra := map[string]any{}
	dirs, _, derr := s.Client.NetworkMaps.Directories(ctx, in.NetworkID)
	switch {
	case derr != nil && errors.Is(derr, forward.ErrFeatureGated):
		limits = append(limits, "map directories are switched off for this organization (the NETWORK_MAP_DIRECTORIES setting), so rows carry no directory")
	case derr != nil:
		limits = append(limits, "the map directories could not be read ("+derr.Error()+"), so rows carry no directory")
	default:
		var ds []map[string]any
		for _, d := range dirs.Directories {
			for _, id := range d.MapIDs {
				dirOf[string(id)] = d.Path
			}
			ds = append(ds, map[string]any{"path": d.Path, "maps": len(d.MapIDs)})
		}
		shown, total := result.CapRow(ds, 50)
		extra["directories"], extra["directories_total"] = shown, total
	}
	var rows []map[string]any
	for _, m := range maps {
		row := map[string]any{"id": string(m.ID), "name": m.Name, "created_by": m.CreatedBy, "created_at": m.CreatedAt, "updated_by": m.UpdatedBy, "updated_at": m.UpdatedAt}
		if m.Pinned != nil {
			row["pinned"] = *m.Pinned
		}
		if d, ok := dirOf[string(m.ID)]; ok {
			row["directory"] = d
		}
		rows = append(rows, row)
	}
	sort.SliceStable(rows, func(i, j int) bool {
		return strings.ToLower(fmt.Sprint(rows[i]["name"])) < strings.ToLower(fmt.Sprint(rows[j]["name"]))
	})
	limits = append(limits, "the list does not say which devices a map covers: give map_id for one map")
	return rows, extra, limits, nil
}

// oneNetworkMap compares the devices placed on one map with the devices the snapshot has: the stored coverage against what the snapshot can draw.
func oneNetworkMap(ctx context.Context, s *fwd.Session, in topologyInput, sid string, limits []string) ([]map[string]any, map[string]any, []string, error) {
	m, _, err := s.Client.NetworkMaps.GetWithDirectory(ctx, in.NetworkID, in.MapID)
	if err != nil {
		return nil, nil, nil, err
	}
	snap, _, err := s.Client.NetworkMaps.Snapshot(ctx, in.NetworkID, in.MapID, sid)
	if err != nil {
		return nil, nil, nil, err
	}
	have := map[string]forward.NetworkMapSnapshotDevice{}
	for _, d := range snap.Devices {
		have[strings.ToLower(d.Name)] = d
	}
	var missing, broken []string
	for _, d := range m.Devices {
		sd, ok := have[strings.ToLower(d.Name)]
		switch {
		case !ok:
			missing = append(missing, d.Name)
		case sd.CollectionError != "" || sd.ProcessingError != "":
			broken = append(broken, d.Name)
		}
	}
	names := func(xs []string) any {
		shown, total := result.CapRow(xs, 50)
		return map[string]any{"count": total, "names": shown}
	}
	row := map[string]any{"id": string(m.ID), "name": m.Name, "directory": m.Directory, "updated_by": m.UpdatedBy, "updated_at": m.UpdatedAt,
		"devices_placed": len(m.Devices), "devices_in_snapshot": len(m.Devices) - len(missing), "placed_but_not_in_snapshot": names(missing), "in_snapshot_with_collection_or_processing_error": names(broken)}
	limits = append(limits, "devices_placed is the stored coverage of the map; a placed device the snapshot lacks is not drawn in that snapshot (renamed, removed or never collected); names are compared case-insensitively",
		"the snapshot shown is snapshot_id, else the network's newest processed one; Forward may use a different default for a map opened in its own UI")
	return []map[string]any{row}, map[string]any{"map_id": in.MapID}, limits, nil
}
