package fwd

import (
	"strings"
	"testing"
)

// Two device configs are the same when they differ only in noise a device writes on its own (a commit timestamp, a byte count, lone "!" separators, trailing blanks). Same lines in another order is reported apart, and a real difference lists the lines missing from and extra in the result.
func TestCompareConfigs(t *testing.T) {
	want := "## Last commit: 2026-04-09 by admin\ninterface lo0\n ip address 1.1.1.1/32\n!\nCurrent configuration : 6583 bytes\nrouter bgp 1\n"
	cases := []struct {
		name, got, wantVerdict string
		missing, extra         []string
	}{
		{"identical apart from noise", "## Last commit: 2026-10-20 by other\nCurrent configuration : 999 bytes\ninterface lo0   \n ip address 1.1.1.1/32\nrouter bgp 1\n!\n", CompareSame, nil, nil},
		{"same lines other order", "router bgp 1\ninterface lo0\n ip address 1.1.1.1/32\n", CompareOrderOnly, nil, nil},
		{"a line missing and one extra", "interface lo0\n ip address 2.2.2.2/32\nrouter bgp 1\n", CompareDiff, []string{" ip address 1.1.1.1/32"}, []string{" ip address 2.2.2.2/32"}},
	}
	for _, c := range cases {
		r := CompareConfigs(c.got, want)
		if r.Verdict != c.wantVerdict || strings.Join(r.Missing, "|") != strings.Join(c.missing, "|") || strings.Join(r.Extra, "|") != strings.Join(c.extra, "|") {
			t.Errorf("%s: %+v", c.name, r)
		}
	}
	// the corpus's own predictor output is compared the same way: SameAs says whether two outputs agree
	if !SameConfig("a\n!\nb\n", "a\nb\n") || SameConfig("a\nb\n", "b\na\n") {
		t.Errorf("SameConfig is exact after noise is dropped")
	}
}
