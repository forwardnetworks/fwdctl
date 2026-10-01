package skills

import (
	"os"
	"testing"

	"github.com/forwardnetworks/fwdctl/nqelint"
)

// needPrivateTree skips a test that reads files of the private repository (plugin manifests, the public README copy).
func needPrivateTree(t *testing.T) {
	t.Helper()
	if _, err := os.Stat("../dist-github"); err != nil {
		t.Skip("not the private repository")
	}
}

// needCorpora skips a test that exercises NQE authoring data, which a build from the public source does not carry.
func needCorpora(t *testing.T) {
	t.Helper()
	if !nqelint.CorporaPresent() {
		t.Skip("this build carries no NQE authoring data (public source build)")
	}
}
