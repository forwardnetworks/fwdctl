package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// `nqe features` lists every NQE feature flag Forward has, with its org property, default, kind and whether it is on for the check in effect; --features turns flags on for the
// offline checker the way an organization with those org properties would have them.
func TestNQEFeaturesListsEachFlagWithItsOrgPropertyDefaultAndKind(t *testing.T) {
	code, out, errOut := call(t, []string{"nqe", "features"}, "", nil)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	var got struct {
		Features []struct {
			Name        string `json:"name"`
			OrgProperty string `json:"org_property"`
			Default     bool   `json:"default"`
			Kind        string `json:"kind"`
			Enabled     bool   `json:"enabled"`
		} `json:"features"`
		Note string `json:"note"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	by := map[string]bool{}
	for _, f := range got.Features {
		by[f.Name] = f.Enabled
		if f.OrgProperty == "" || f.Kind == "" {
			t.Errorf("incomplete: %+v", f)
		}
	}
	if len(got.Features) != 10 || by["security-rules-panos"] || !by["cis-benchmarks"] || !by["trim-builtin"] {
		t.Errorf("features = %v", by)
	}
	if !strings.Contains(got.Note, "NQE_ORDERED_LIST") {
		t.Errorf("the note should say ordered lists are retired: %q", got.Note)
	}
	_, out, _ = call(t, []string{"nqe", "features", "--features", "security-rules-fortios,device-services"}, "", nil)
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	on := map[string]bool{}
	for _, f := range got.Features {
		on[f.Name] = f.Enabled
	}
	if !on["security-rules-fortios"] || !on["device-services"] || on["security-rules-panos"] {
		t.Errorf("with --features: %v", on)
	}
}

func TestNQELintFeaturesFlagMakesAGatedFieldValidAndEnvDoesToo(t *testing.T) {
	const q = "foreach device in network.devices select {p: device.securityPolicy}"
	code, out, _ := call(t, []string{"nqe", "lint", "-"}, q, nil)
	if code != 1 || !strings.Contains(out, "NQE_SECURITY_RULES_PANOS") || !strings.Contains(out, "--features security-rules-panos") {
		t.Errorf("by default the field is behind a flag and the diagnostic says which: %d %s", code, out)
	}
	if code, out, _ = call(t, []string{"nqe", "lint", "--features", "security-rules-panos", "-"}, q, nil); code != 0 || !strings.Contains(out, `"valid": true`) {
		t.Errorf("--features turns it on: %d %s", code, out)
	}
	t.Setenv("FORWARD_NQE_FEATURES", "NQE_SECURITY_RULES_FORTIOS")
	if code, out, _ = call(t, []string{"nqe", "lint", "-"}, q, nil); code != 0 {
		t.Errorf("FORWARD_NQE_FEATURES (an org property name) turns it on: %d %s", code, out)
	}
	t.Setenv("FORWARD_NQE_FEATURES", "")
	if code, out, errOut := call(t, []string{"nqe", "lint", "--features", "no-such", "-"}, q, nil); code == 0 || !strings.Contains(out+errOut, "security-rules-panos") {
		t.Errorf("an unknown feature is refused with the list: %d %s %s", code, out, errOut)
	}
}

// context schema shows Forward's FULL model (an author can find securityPolicy) unless features are asked for, and then it shows what an organization with those flags sees.
func TestContextSchemaIsTheFullModelUnlessFeaturesAreGiven(t *testing.T) {
	_, out, _ := call(t, []string{"context", "schema", "securityPolicy"}, "", nil)
	if !strings.Contains(out, "network.devices.securityPolicy") {
		t.Errorf("the full model has securityPolicy: %s", out[:min(len(out), 300)])
	}
	_, out, _ = call(t, []string{"context", "schema", "securityPolicy", "--features", "none"}, "", nil)
	if strings.Contains(out, "network.devices.securityPolicy.scopes") {
		t.Errorf("with every feature off the security policy is not in the model: %s", out[:min(len(out), 300)])
	}
}

// fwdctl's own experimental lint rules are listed beside Forward's feature flags and turned on with --experimental (or FORWARD_NQE_EXPERIMENTAL); a rule only warns.
func TestNQELintExperimentalFlagTurnsOnAPerfRuleAndFeaturesListsTheRules(t *testing.T) {
	const q = "foreach device in network.devices\nforeach command in device.outputs.commands\nselect {n: length(regexMatches(command.response, re`x (?<y>\\S+)`))}"
	code, out, _ := call(t, []string{"nqe", "lint", "-"}, q, nil)
	if code != 0 || strings.Contains(out, "experimental-perf-command-response") {
		t.Errorf("off by default: %d %s", code, out)
	}
	code, out, _ = call(t, []string{"nqe", "lint", "--experimental", "perf-command-response", "-"}, q, nil)
	if code != 0 || !strings.Contains(out, "experimental-perf-command-response") || !strings.Contains(out, `"valid": true`) {
		t.Errorf("--experimental warns and the query stays valid: %d %s", code, out)
	}
	t.Setenv("FORWARD_NQE_EXPERIMENTAL", "all")
	if _, out, _ = call(t, []string{"nqe", "lint", "-"}, q, nil); !strings.Contains(out, "experimental-perf-command-response") {
		t.Errorf("FORWARD_NQE_EXPERIMENTAL: %s", out)
	}
	t.Setenv("FORWARD_NQE_EXPERIMENTAL", "")
	_, out, _ = call(t, []string{"nqe", "features"}, "", nil)
	var got struct {
		Rules []struct {
			Name, Evidence string
			Enabled        bool
		} `json:"experimental_rules"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil || len(got.Rules) == 0 || got.Rules[0].Name != "perf-command-response" || got.Rules[0].Evidence == "" || got.Rules[0].Enabled {
		t.Errorf("features lists the experimental rules: %v %s", err, out)
	}
	if code, out, errOut := call(t, []string{"nqe", "lint", "--experimental", "bogus", "-"}, q, nil); code == 0 || !strings.Contains(out+errOut, "perf-command-response") {
		t.Errorf("an unknown rule is refused with the list: %d %s %s", code, out, errOut)
	}
}

// `nqe features` marks the two security-rules features experimental in the data and says so in its help, so nobody reads device.securityPolicy as standard.
func TestNQEFeaturesMarksTheSecurityRulesFeaturesExperimental(t *testing.T) {
	_, out, _ := call(t, []string{"nqe", "features"}, "", nil)
	var got struct {
		Features []struct {
			Name         string `json:"name"`
			Experimental bool   `json:"experimental"`
			Enabled      bool   `json:"enabled"`
		} `json:"features"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, f := range got.Features {
		rules := f.Name == "security-rules-panos" || f.Name == "security-rules-fortios"
		if f.Experimental != rules || (rules && f.Enabled) {
			t.Errorf("%s experimental=%v enabled=%v", f.Name, f.Experimental, f.Enabled)
		}
		if rules {
			n++
		}
	}
	_, help, _ := call(t, []string{"nqe", "features", "--help"}, "", nil)
	if n != 2 || !strings.Contains(help, "EXPERIMENTAL") || !strings.Contains(help, "NQE_SECURITY_RULES_PANOS") || !strings.Contains(help, "26.10") {
		t.Errorf("help: %s", help)
	}
}
