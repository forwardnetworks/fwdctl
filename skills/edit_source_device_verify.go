package skills

import (
	"encoding/json"
	"math"
	"reflect"
	"sort"

	forward "github.com/forwardnetworks/forward-go-sdk"
)

// deviceHolds reads a classic device back against the patch that was sent: every property the patch set must read as sent, and every property it cleared (an explicit null) must be absent or null. The name is checked by
// finding the device under it. A property the read does not carry at all is reported, not assumed.
func deviceHolds(d forward.ClassicDevice, patch forward.ClassicDevicePatch) (bool, []string) {
	b, err := json.Marshal(patch)
	if err != nil {
		return false, []string{"the patch could not be re-read: " + err.Error()}
	}
	var sent map[string]json.RawMessage
	if json.Unmarshal(b, &sent) != nil {
		return false, []string{"the patch could not be re-read"}
	}
	var bad []string
	for k, want := range sent {
		if k == "name" {
			continue
		}
		got, has := d.Raw[k]
		if string(want) == "null" {
			if has && string(got) != "null" {
				bad = append(bad, k+" (cleared, still set)")
			}
			continue
		}
		if !has {
			bad = append(bad, k+" (not on the record)")
			continue
		}
		var w, g any
		if json.Unmarshal(want, &w) != nil || json.Unmarshal(got, &g) != nil || !sameJSONValue(w, g) {
			bad = append(bad, k+" (does not read as sent)")
		}
	}
	sort.Strings(bad)
	return len(bad) == 0, bad
}

func sameJSONValue(a, b any) bool {
	fa, aok := a.(float64)
	fb, bok := b.(float64)
	if aok && bok {
		return math.Abs(fa-fb) < 1e-6
	}
	return reflect.DeepEqual(a, b)
}
