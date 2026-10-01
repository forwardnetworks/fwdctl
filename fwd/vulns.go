package fwd

import (
	"context"
	"sort"

	forward "github.com/forwardnetworks/forward-go-sdk"
)

// SeverityRank orders NVD severities; unknown and NONE are lowest.
func SeverityRank(s string) int {
	switch s {
	case "CRITICAL":
		return 4
	case "HIGH":
		return 3
	case "MEDIUM":
		return 2
	case "LOW":
		return 1
	}
	return 0
}

// Exposed reports a detection result that means the device is vulnerable as far as Forward can tell:
// VULNERABLE (confirmed from the configuration) or OS_VULNERABLE (the OS version is affected and the CVE does
// not depend on configuration).
func Exposed(result string) bool { return result == "VULNERABLE" || result == "OS_VULNERABLE" }

// Potential reports a result Forward could not settle: UNCONFIRMED (depends on configuration it could not
// confirm) or UNIMPLEMENTED (no configuration analysis for this CVE yet). Neither is a pass.
func Potential(result string) bool { return result == "UNCONFIRMED" || result == "UNIMPLEMENTED" }

// CVE is one CVE's exposure across the network, summed over the affected OSes.
type CVE struct {
	ID           string         `json:"id"`
	Description  string         `json:"description,omitempty"`
	KnownExploit bool           `json:"known_exploit"`
	Severity     string         `json:"severity"`
	TopScore     *float64       `json:"top_score,omitempty"`
	Results      map[string]int `json:"results"` // detection result -> device count
	OSes         []string       `json:"oses,omitempty"`
}

// ExposedDevices counts devices with an exposed result.
func (c CVE) ExposedDevices() int {
	n := 0
	for r, k := range c.Results {
		if Exposed(r) {
			n += k
		}
	}
	return n
}

// PotentialDevices counts devices Forward could not settle.
func (c CVE) PotentialDevices() int {
	n := 0
	for r, k := range c.Results {
		if Potential(r) {
			n += k
		}
	}
	return n
}

// NetworkVulnerabilities lists the CVEs that may affect a network, worst first: highest severity, then known
// exploited, then most exposed devices. indexInfo says which CVE index the answer came from.
func (s *Session) NetworkVulnerabilities(ctx context.Context, networkID, snapshotID string, internet *bool) ([]CVE, string, error) {
	a, _, err := s.Client.Vulnerabilities.List(ctx, networkID, forward.VulnerabilityListOptions{SnapshotID: snapshotID, InternetAddressable: internet})
	if err != nil {
		return nil, "", err
	}
	s.Annotate(intp(len(a.Vulnerabilities)), false)
	out := make([]CVE, 0, len(a.Vulnerabilities))
	for _, v := range a.Vulnerabilities {
		c := CVE{ID: v.ID, Description: v.Description, KnownExploit: v.HasCISAKEVEntry, Severity: "NONE", Results: map[string]int{}}
		for _, o := range v.OSInfos {
			if SeverityRank(string(o.Severity)) > SeverityRank(c.Severity) {
				c.Severity = string(o.Severity)
			}
			if o.AdvisoryMentionsExploit {
				c.KnownExploit = true
			}
			for _, sc := range []*float64{o.V4Score, o.V3Score, o.V2Score} {
				if sc != nil && (c.TopScore == nil || *sc > *c.TopScore) {
					v := *sc
					c.TopScore = &v
				}
			}
			for _, rc := range o.ResultCounts {
				c.Results[string(rc.Result)] += rc.DeviceCount
			}
			if o.OS != "" {
				c.OSes = append(c.OSes, o.OS)
			}
		}
		out = append(out, c)
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if ra, rb := SeverityRank(a.Severity), SeverityRank(b.Severity); ra != rb {
			return ra > rb
		}
		if a.KnownExploit != b.KnownExploit {
			return a.KnownExploit
		}
		return a.ExposedDevices() > b.ExposedDevices()
	})
	info := a.IndexCreatedAt
	return out, info, nil
}

// CVEDevice is one device's verdict for one CVE.
type CVEDevice struct {
	Name                string `json:"name"`
	OS                  string `json:"os,omitempty"`
	OSVersion           string `json:"os_version,omitempty"`
	Result              string `json:"result"`
	Status              string `json:"status"`
	InternetAddressable *bool  `json:"internet_addressable"`
	Config              bool   `json:"config_evidence"` // true when Forward cites configuration lines
}

// CVEDetail is one CVE with a verdict per affected device.
type CVEDetail struct {
	CVE     CVE
	Devices []CVEDevice
}

// CVEDetail reads one CVE with its per-device verdicts. found is false when Forward has no such CVE among
// those that may affect the network (it does not say whether the CVE exists at all).
func (s *Session) CVEDetail(ctx context.Context, networkID, cveID, snapshotID string) (CVEDetail, bool, error) {
	v, _, err := s.Client.Vulnerabilities.Get(ctx, networkID, cveID, snapshotID)
	if forwardNotFound(err) {
		return CVEDetail{}, false, nil
	}
	if err != nil {
		return CVEDetail{}, false, err
	}
	d := CVEDetail{CVE: CVE{ID: v.ID, Description: v.Description, KnownExploit: v.HasCISAKEVEntry, Severity: "NONE", Results: map[string]int{}}}
	for _, o := range v.OSInfos {
		if SeverityRank(string(o.Severity)) > SeverityRank(d.CVE.Severity) {
			d.CVE.Severity = string(o.Severity)
		}
		if o.AdvisoryMentionsExploit {
			d.CVE.KnownExploit = true
		}
		for _, sc := range []*float64{o.V4Score, o.V3Score, o.V2Score} {
			if sc != nil && (d.CVE.TopScore == nil || *sc > *d.CVE.TopScore) {
				x := *sc
				d.CVE.TopScore = &x
			}
		}
		if d.CVE.Description == "" {
			d.CVE.Description = o.Description
		}
		for _, dev := range o.Devices {
			d.CVE.Results[string(dev.Result)]++
			d.Devices = append(d.Devices, CVEDevice{Name: dev.Name, OS: o.OS, OSVersion: dev.OSVersion, Result: string(dev.Result),
				Status: string(dev.Status), InternetAddressable: dev.InternetAddressable, Config: len(dev.FileRanges) > 0})
		}
		if o.OS != "" {
			d.CVE.OSes = append(d.CVE.OSes, o.OS)
		}
	}
	s.Annotate(intp(len(d.Devices)), false)
	sort.SliceStable(d.Devices, func(i, j int) bool {
		a, b := d.Devices[i], d.Devices[j]
		ea, eb := Exposed(a.Result), Exposed(b.Result)
		if ea != eb {
			return ea
		}
		return a.Name < b.Name
	})
	return d, true, nil
}
