package skills

import (
	"context"

	forward "github.com/forwardnetworks/forward-go-sdk"

	"github.com/forwardnetworks/fwdctl/fwd"
	"github.com/forwardnetworks/fwdctl/result"
)

// effectiveBoostDefault is Forward's default per-device concurrency boost (client-structs OrgCollectionSettings, read by a peer session: the getter falls back to 0). The SDK has no constant for it.
const effectiveBoostDefault = 0

// maxEffectiveCollectors bounds the per-collector settings reads.
const maxEffectiveCollectors = 200

type settingRow struct {
	name string
	val  *int
	def  int
}

func settingRows(scope string, items []settingRow) []any {
	out := make([]any, 0, len(items))
	for _, it := range items {
		row := map[string]any{"scope": scope, "setting": it.name, "default": it.def}
		if it.val != nil {
			row["value"], row["source"], row["differs"] = *it.val, "set", *it.val != it.def
		} else {
			row["value"], row["source"], row["differs"] = it.def, "default", false
		}
		out = append(out, row)
	}
	return out
}

// effectiveSettingRows is inspect-platform area collection_settings, view effective: every collection setting with its value, Forward's default, whether it was set and whether it differs, for the organization and each
// collector. Forward returns only what is stored; the defaults are the SDK's constants (client-structs), so an absent field reads as its default.
func effectiveSettingRows(ctx context.Context, s *fwd.Session) ([]any, []string, int, error) {
	org, _, err := s.Client.Collectors.GetOrganizationSettings(ctx)
	if err != nil {
		return nil, nil, 0, err
	}
	rows := settingRows("organization", []settingRow{
		{"maxCommandAuthZPerSecond", org.MaxCommandAuthZPerSecond, forward.DefaultMaxCommandAuthZPerSecond},
		{"maxDeviceAuthNPerSecond", org.MaxDeviceAuthNPerSecond, forward.DefaultMaxDeviceAuthNPerSecond},
		{"maxScanConnectionsPerSecond", org.MaxScanConnectionsPerSecond, forward.DefaultMaxScanConnectionsPerSecond},
		{"perDeviceConcurrencyBoost", org.PerDeviceConcurrencyBoost, effectiveBoostDefault},
		{"perDeviceSnmpConcurrency", org.PerDeviceSNMPConcurrency, forward.DefaultPerDeviceSNMPConcurrency},
		{"deviceCollectionTimeoutMinutes", org.DeviceCollectionTimeoutMins, forward.DefaultDeviceCollectionTimeoutMinutes},
		{"deviceDiscoveryTimeoutMinutes", org.DeviceDiscoveryTimeoutMins, forward.DefaultDeviceDiscoveryTimeoutMinutes},
		{"generalJobTimeoutMinutes", org.GeneralJobTimeoutMins, forward.DefaultGeneralJobTimeoutMinutes},
		{"snapshotCollectionTimeoutMinutes", org.SnapshotCollectionTimeoutMins, forward.DefaultSnapshotCollectionTimeoutMinutes},
		{"collectionRetries", org.CollectionRetries, forward.DefaultCollectionRetries},
		{"collectionRetryDelayMillis", org.CollectionRetryDelayMillis, forward.DefaultCollectionRetryDelayMillis},
		{"collectionMaxRetryDelayMillis", org.CollectionMaxRetryDelayMillis, forward.DefaultCollectionMaxRetryDelayMillis},
	})
	// settings whose default this tool does not know are shown only when set, with no verdict
	if org.CommandDelayMS != nil {
		rows = append(rows, map[string]any{"scope": "organization", "setting": "commandDelayMs", "value": *org.CommandDelayMS, "source": "set", "default": nil, "differs": nil})
	}
	for name, b := range map[string]*bool{"enableBetaCommands": org.EnableBetaCommands, "disableCommandOutputLogging": org.DisableCommandOutputLogging, "enableSnmpCredDiscovery": org.EnableSNMPCredDiscovery} {
		if b != nil {
			rows = append(rows, map[string]any{"scope": "organization", "setting": name, "value": *b, "source": "set", "default": nil, "differs": nil})
		}
	}
	var limits []string
	cs, _, cerr := s.Client.Collectors.List(ctx)
	if limits = soft(limits, "the collector list", cerr); cerr == nil {
		var om []result.Omission
		cs, om = result.Cap(cs, maxEffectiveCollectors, "collectors", "the rest were not read")
		for _, o := range om {
			limits = append(limits, o.Text())
		}
		for _, c := range cs {
			cset, _, gerr := s.Client.Collectors.GetSettings(ctx, string(c.ID))
			if limits = soft(limits, "collector "+c.Name+"'s settings", gerr); gerr != nil {
				continue
			}
			rows = append(rows, settingRows("collector "+c.Name, []settingRow{
				{"concurrency", cset.Concurrency, forward.DefaultCollectorConcurrency},
				{"snmpCollectionConcurrency", cset.SNMPCollectionConcurrency, forward.DefaultCollectorSNMPConcurrency},
				{"vcenterConcurrency", cset.VCenterConcurrency, forward.DefaultCollectorVCenterConcurrency},
			})...)
		}
	}
	differ := 0
	for _, r := range rows {
		if m := r.(map[string]any); m["differs"] == true {
			differ++
		}
	}
	limits = append(limits,
		"a setting Forward did not return is not set, so Forward uses its default (shown as source default): an absent timeout means the default timeout, not no timeout",
		"defaults are Forward's compiled defaults as of the SDK; commandDelayMs and the on/off switches are listed only when set, because their defaults are not known here",
		"organization properties that may also govern collection timeouts are in area org_properties, which needs a login with Forward support authority; this view cannot say which are set")
	return rows, limits, differ, nil
}
