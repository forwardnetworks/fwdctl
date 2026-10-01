package skills_test

import (
	"os"
	"testing"

	"github.com/forwardnetworks/fwdctl/nqelint"
)

// needCorpora skips a test that exercises Forward's NQE data model, builtin table or examples, which a build from the public source does not carry.
func needCorpora(t *testing.T) {
	t.Helper()
	if !nqelint.CorporaPresent() {
		t.Skip("this build carries no NQE data (public source build)")
	}
}

// needPrivateTree skips a test that reads files of the private repository (plugin manifests, the public README copy).
func needPrivateTree(t *testing.T) {
	t.Helper()
	if _, err := os.Stat("../dist-github"); err != nil {
		t.Skip("not the private repository")
	}
}
