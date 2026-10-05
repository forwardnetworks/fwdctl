package skills

import "testing"

func TestPortKeyMatchesLabAndForwardNames(t *testing.T) {
	for _, c := range [][2]string{{"Ethernet0/2", "eth0/2"}, {"GigabitEthernet1/0/1", "Gi1/0/1"}, {"Port-Channel5.100", "po5.100"}} {
		if portKey(c[0]) != portKey(c[1]) {
			t.Errorf("%s and %s are one port", c[0], c[1])
		}
	}
	if portKey("eth0/2") == portKey("eth0/3") || !samePort("Loopback0", "lo0") {
		t.Errorf("different ports stay different")
	}
}
