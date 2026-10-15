package skills_test

import (
	"strings"
	"testing"
	"time"

	"github.com/forwardnetworks/fwdctl/result"
)

// kind lifecycle reads each device's OS support dates (Forward's lifecycle data) and rolls them up: support ended, ending within 12 months, later, and no record at all, by vendor and OS, with the devices
// whose support has ended named. A device with no record is not "supported": the roll-up says so as its own count.
func lifeRow(dev, vendor, os, last string) map[string]any {
	m := map[string]any{"Device": dev, "Vendor": vendor, "OS": os, "OSVersion": "1"}
	if last != "" {
		m["LastSupportDate"] = last
		m["LastMaintenanceDate"] = last
	}
	return m
}

func TestInventoryLifecycleRollsUpSupportEndAndMissingRecords(t *testing.T) {
	rows := []any{
		lifeRow("old1", "CISCO", "IOS", "2020-01-31"),
		lifeRow("old2", "CISCO", "IOS", "2021-06-30"),
		lifeRow("soon", "ARISTA", "EOS", time.Now().AddDate(0, 6, 0).Format("2006-01-02")),
		lifeRow("fine", "ARISTA", "EOS", "2099-12-31"),
		lifeRow("none", "LINUX", "LINUX", ""),
	}
	r, _ := mustRun(t, "inspect-inventory", coverageRoutes(rows, []any{}), `{"network_id":"n1","kind":"lifecycle"}`)
	if r.Status != result.OK || !strings.Contains(r.Finding, "2 of 5") || !strings.Contains(r.Finding, "past its last support date") {
		t.Fatalf("%s %q", r.Status, r.Finding)
	}
	d := r.Evidence[0].Detail
	if d["devices"] != 5 || d["support_ended"] != 2 || d["no_record"] != 1 || d["supported_later"] != 1 || d["ending_within_12_months"] != 1 {
		t.Fatalf("detail = %v", d)
	}
	ended, _ := d["ended"].([]map[string]any)
	if len(ended) != 2 || ended[0]["device"] != "old1" {
		t.Errorf("ended = %v", ended)
	}
	groups, _ := d["by_os"].([]map[string]any)
	if len(groups) == 0 || groups[0]["vendor"] != "CISCO" || groups[0]["support_ended"] != 2 {
		t.Errorf("by_os = %v", groups)
	}
}
