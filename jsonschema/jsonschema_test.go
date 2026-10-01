package jsonschema

import (
	"encoding/json"
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
