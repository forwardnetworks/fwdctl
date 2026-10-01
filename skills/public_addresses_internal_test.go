package skills

import (
	"net/netip"
	"strings"
	"testing"
)

func TestAggregateV4IsExact(t *testing.T) {
	ip := func(s ...string) []netip.Addr {
		var o []netip.Addr
		for _, x := range s {
			o = append(o, netip.MustParseAddr(x))
		}
		return o
	}
	for _, c := range []struct {
		in   []netip.Addr
		want string
	}{
		{ip("192.0.2.0", "192.0.2.1", "192.0.2.2", "192.0.2.3"), "192.0.2.0/30"},
		{ip("192.0.2.1", "192.0.2.2"), "192.0.2.1/32 192.0.2.2/32"}, // not aligned: no wider block
		{ip("192.0.2.5", "192.0.2.5", "192.0.2.4"), "192.0.2.4/31"},
		{ip("192.0.2.255", "192.0.3.0"), "192.0.2.255/32 192.0.3.0/32"},
		{ip("192.0.2.0", "192.0.2.1", "192.0.2.2", "192.0.2.3", "192.0.2.4", "192.0.2.5", "192.0.2.6", "192.0.2.7", "192.0.2.8"), "192.0.2.0/29 192.0.2.8/32"},
		{ip("255.255.255.254", "255.255.255.255"), "255.255.255.254/31"},
	} {
		var got []string
		for _, p := range aggregateV4(c.in) {
			got = append(got, p.String())
		}
		if strings.Join(got, " ") != c.want {
			t.Errorf("%v: got %v, want %s", c.in, got, c.want)
		}
	}
}
