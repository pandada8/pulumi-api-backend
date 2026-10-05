package pulumicompat

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestJSON(t *testing.T) {
	for _, s := range []string{`{"a":1,"a":2}`, `{"a":{"b":1,"b":2}}`, `[] {}`, `{"a":`} {
		var v any
		if e := Decode([]byte(s), &v); e == nil {
			t.Fatal("accepted", s)
		}
	}
	var m map[string]any
	if e := Decode([]byte(`{"n":9007199254740993}`), &m); e != nil {
		t.Fatal(e)
	}
	if m["n"].(json.Number).String() != "9007199254740993" {
		t.Fatal("precision")
	}
	if e := Validate(Empty()); e != nil {
		t.Fatal(e)
	}
}
func TestRenameAndRedaction(t *testing.T) {
	urn := "urn:pulumi:dev::wire::backendtest:index:Item::r"
	raw := []byte(`{"version":3,"deployment":{"manifest":{},"unknown":123,"resources":[{"urn":"` + urn + `","type":"backendtest:index:Item","provider":"urn:pulumi:dev::wire::pulumi:providers:backendtest::p::provider-id","inputs":{"plain":"` + urn + `","reference":{"` + sigKey + `":"` + refSig + `","urn":"` + urn + `"},"password":{"` + sigKey + `":"` + secretSig + `","ciphertext":"hidden"}},"outputs":{"extra":"sensitive"},"additionalSecretOutputs":["extra"]}]}}`)
	out, e := Rename(raw, "demo", "wire", "dev", "newproject", "prod", "https://api.test")
	if e != nil {
		t.Fatal(e)
	}
	var u map[string]any
	Decode(out, &u)
	d := u["deployment"].(map[string]any)
	r := d["resources"].([]any)[0].(map[string]any)
	if r["urn"] == urn || !strings.HasSuffix(r["provider"].(string), "::provider-id") {
		t.Fatal("identity rewrite")
	}
	if r["inputs"].(map[string]any)["plain"] != urn {
		t.Fatal("application string changed")
	}
	b, _ := json.Marshal(Redact(u))
	if strings.Contains(string(b), "hidden") || strings.Contains(string(b), "sensitive") {
		t.Fatal("secret leak")
	}
	if d["unknown"].(json.Number).String() != "123" {
		t.Fatal("unknown field lost")
	}
}
func TestDiffIdentitiesAndSecrets(t *testing.T) {
	before := []byte(`{"version":3,"deployment":{"resources":[{"urn":"urn:pulumi:dev::p::t::r","id":"old","outputs":{"password":{"` + sigKey + `":"` + secretSig + `","ciphertext":"first"}}}]}}`)
	after := []byte(`{"version":3,"deployment":{"resources":[{"urn":"urn:pulumi:dev::p::t::r","id":"new","outputs":{"password":{"` + sigKey + `":"` + secretSig + `","ciphertext":"second"}}}]}}`)
	diff, e := Diff(before, after)
	if e != nil || len(diff) != 2 {
		t.Fatal(e, diff)
	}
	actions := map[string]bool{}
	for _, d := range diff {
		actions[d.Action] = true
	}
	if !actions["create"] || !actions["delete"] {
		t.Fatal("replacement conflated", diff)
	}
	after = []byte(strings.ReplaceAll(string(after), `"id":"new"`, `"id":"old"`))
	diff, e = Diff(before, after)
	if e != nil || len(diff) != 1 || diff[0].Action != "update" || len(diff[0].Paths) != 1 || diff[0].Paths[0] != "/outputs/password" {
		t.Fatal("secret path", diff, e)
	}
	b, _ := json.Marshal(diff)
	if strings.Contains(string(b), "first") || strings.Contains(string(b), "second") {
		t.Fatal("diff leak")
	}
}
func TestFeatureScope(t *testing.T) {
	raw := Empty()
	var m map[string]any
	Decode(raw, &m)
	m["deployment"].(map[string]any)["resources"] = []any{map[string]any{"urn": "urn:pulumi:dev::wire::backendtest:index:Item::r", "type": "backendtest:index:Item", "inputs": map[string]any{"hooks": "application data"}}}
	b, _ := json.Marshal(m)
	if e := Validate(b); e != nil {
		t.Fatal("application property rejected", e)
	}
	m["deployment"].(map[string]any)["resources"].([]any)[0].(map[string]any)["hooks"] = map[string]any{}
	b, _ = json.Marshal(m)
	if e := Validate(b); e == nil {
		t.Fatal("v4 resource feature accepted")
	}
}
