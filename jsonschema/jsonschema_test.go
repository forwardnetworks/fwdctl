package jsonschema

import (
	"encoding/json"
	"strings"
	"testing"
)

const sch = `{"type":"object","required":["network_id"],"additionalProperties":false,
 "properties":{"network_id":{"type":"string","minLength":1},"protocol":{"type":["string","integer"]},
 "max":{"type":"integer","minimum":1},"mode":{"enum":["a","b"]},
 "xs":{"type":"array","items":{"type":"string"}}}}`

func TestValidAndInvalidInputs(t *testing.T) {
	ok := []string{`{"network_id":"n"}`, `{"network_id":"n","protocol":6,"max":3,"mode":"a","xs":["q"]}`, `{"network_id":"n","protocol":"tcp"}`}
	for _, in := range ok {
		if e, err := Validate([]byte(sch), []byte(in)); err != nil || len(e) != 0 {
			t.Errorf("%s: %v %v", in, e, err)
		}
	}
	// POSITIVE CONTROL: the validator must be able to say no.
	bad := []string{`{}`, `{"network_id":""}`, `{"network_id":"n","extra":1}`, `{"network_id":"n","max":0}`,
		`{"network_id":"n","max":1.5}`, `{"network_id":"n","mode":"c"}`, `{"network_id":"n","xs":[1]}`, `[1]`, `not json`}
	for _, in := range bad {
		if e, err := Validate([]byte(sch), []byte(in)); err != nil || len(e) == 0 {
			t.Errorf("%s was accepted (%v, %v)", in, e, err)
		}
	}
}

func TestUnsupportedKeywordIsAnErrorNotSilentlyIgnored(t *testing.T) {
	if _, err := Validate([]byte(`{"oneOf":[]}`), json.RawMessage(`{}`)); err == nil {
		t.Error("unsupported keyword was ignored")
	}
}

// A required property that is an enumeration says what its values are, so a caller who left it out learns the options from the error instead of a second round trip.
func TestMissingRequiredEnumPropertyListsItsValues(t *testing.T) {
	const s = `{"type":"object","required":["kind"],"properties":{"kind":{"enum":["summary","devices","cloud_coverage"]},"n":{"type":"string"}}}`
	errs, err := Validate([]byte(s), []byte(`{"n":"x"}`))
	if err != nil || len(errs) != 1 {
		t.Fatalf("%v %v", errs, err)
	}
	for _, want := range []string{`missing required "kind"`, "summary", "devices", "cloud_coverage"} {
		if !strings.Contains(errs[0], want) {
			t.Errorf("the error lacks %q: %s", want, errs[0])
		}
	}
	// a required property that is not an enumeration keeps the plain message
	errs, _ = Validate([]byte(`{"type":"object","required":["n"],"properties":{"n":{"type":"string"}}}`), []byte(`{}`))
	if len(errs) != 1 || strings.Contains(errs[0], "one of") {
		t.Errorf("plain required: %v", errs)
	}
}

// A type error names the JSON types in words a person can act on: "expected a string, got an array", not Go's "[string]" and "[]interface {}".
func TestTypeErrorsNameJSONTypes(t *testing.T) {
	e, err := Validate([]byte(sch), []byte(`{"network_id":["a","b"]}`))
	if err != nil || len(e) != 1 || !strings.Contains(e[0], "expected a string, got an array") {
		t.Fatalf("%v %v", e, err)
	}
	e, _ = Validate([]byte(sch), []byte(`{"network_id":"n","protocol":{"x":1}}`))
	if len(e) != 1 || !strings.Contains(e[0], "expected a string or an integer, got an object") {
		t.Errorf("%v", e)
	}
	e, _ = Validate([]byte(sch), []byte(`{"network_id":"n","max":"3"}`))
	if len(e) != 1 || !strings.Contains(e[0], "expected an integer, got a string") {
		t.Errorf("%v", e)
	}
}

// x-values is an annotation: the one-line meaning of each value of an enum, shown by `fwdctl run NAME --help`. The validator accepts it and does not check against it.
func TestXValuesIsAnAnnotation(t *testing.T) {
	s := `{"type":"object","properties":{"view":{"enum":["a","b"],"x-values":{"a":"first","b":"second"}}}}`
	if e, err := Validate([]byte(s), []byte(`{"view":"a"}`)); err != nil || len(e) != 0 {
		t.Errorf("%v %v", e, err)
	}
	if e, _ := Validate([]byte(s), []byte(`{"view":"c"}`)); len(e) == 0 {
		t.Errorf("an enum value outside the list is still refused")
	}
}
