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

func TestDiffMissingVersusNull(t *testing.T) {
	a := []byte(`{"deployment":{"resources":[{"urn":"x","outputs":{}}]}}`)
	b := []byte(`{"deployment":{"resources":[{"urn":"x","outputs":{"value":null}}]}}`)
	d, e := Diff(a, b)
	if e != nil || len(d) != 1 || len(d[0].Paths) != 1 || d[0].Paths[0] != "/outputs/value" {
		t.Fatalf("missing vs null: %#v %v", d, e)
	}
}

func TestDeploymentV4(t *testing.T) {
	const urn = "urn:pulumi:dev::wire::pkg:index:Thing::r"
	for _, field := range []string{`"viewOf":"` + urn + `"`, `"resourceHooks":{"beforeCreate":["hook"]}`, `"taint":true`, `"replaceWith":["` + urn + `"]`, `"refreshBeforeUpdate":true`} {
		raw := []byte(`{"version":4,"features":["views","hooks","taint","replaceWith","refreshBeforeUpdate"],"deployment":{"manifest":{},"resources":[{"urn":"` + urn + `","type":"pkg:index:Thing",` + field + `}]}}`)
		if e := Validate(raw); e != nil {
			t.Fatal(field, e)
		}
		var m map[string]any
		Decode(raw, &m)
		m["version"] = 3
		delete(m, "features")
		downgraded, _ := json.Marshal(m)
		if Validate(downgraded) == nil {
			t.Fatal("v4 field accepted in v3", field)
		}
	}
	for _, envelope := range []string{`"version":5`, `"version":3,"features":["views"]`, `"version":4,"features":["future"]`} {
		if Validate([]byte(`{`+envelope+`,"deployment":{"manifest":{}}}`)) == nil {
			t.Fatal("unsupported envelope", envelope)
		}
	}
	if Validate([]byte(`{"version":4,"deployment":{"manifest":{},"resources":[{"urn":"`+urn+`","type":"pkg:index:Thing","taint":"yes"}]}}`)) == nil {
		t.Fatal("invalid feature type accepted")
	}
}

func TestRenameV4References(t *testing.T) {
	const urn = "urn:pulumi:dev::wire::pkg:index:Thing::r"
	const newURN = "urn:pulumi:prod::newproject::pkg:index:Thing::r"
	r := map[string]any{"urn": urn, "type": "pkg:index:Thing", "viewOf": urn, "replaceWith": []any{urn}, "inputs": map[string]any{"plain": urn}}
	raw, _ := json.Marshal(map[string]any{"version": 4, "features": []string{"views", "replaceWith", "snippets-prototype"}, "deployment": map[string]any{"manifest": map[string]any{}, "resources": []any{r}, "pending_operations": []any{map[string]any{"type": "creating", "resource": r}}, "snippets": []any{map[string]any{"name": "s", "code": urn, "references": map[string]any{"r": urn}}}}})
	out, e := Rename(raw, "demo", "wire", "dev", "newproject", "prod", "https://api.test")
	if e != nil {
		t.Fatal(e)
	}
	var m map[string]any
	Decode(out, &m)
	d := m["deployment"].(map[string]any)
	for _, r := range []map[string]any{d["resources"].([]any)[0].(map[string]any), d["pending_operations"].([]any)[0].(map[string]any)["resource"].(map[string]any)} {
		if r["viewOf"] != newURN || r["replaceWith"].([]any)[0] != newURN || r["inputs"].(map[string]any)["plain"] != urn {
			t.Fatal("reference rewrite", r)
		}
	}
	snippet := d["snippets"].([]any)[0].(map[string]any)
	if snippet["references"].(map[string]any)["r"] != newURN || snippet["code"] != urn || len(m["features"].([]any)) != 3 {
		t.Fatal("snippet or envelope changed", m)
	}
}
