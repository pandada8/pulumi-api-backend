package pulumicompat

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/pulumi/pulumi/sdk/v3/go/common/apitype"
	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
	"github.com/pulumi/pulumi/sdk/v3/go/common/tokens"
	"io"
	"time"
)

const Version = "3.246.0"

// Decode rejects duplicate keys and trailing values, preserving JSON number precision.
func Decode(raw []byte, out any) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if e := scan(d); e != nil {
		return e
	}
	if _, e := d.Token(); e != io.EOF {
		return fmt.Errorf("trailing JSON")
	}
	d = json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	return d.Decode(out)
}
func scan(d *json.Decoder) error {
	t, e := d.Token()
	if e != nil {
		return e
	}
	delim, ok := t.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := map[string]bool{}
		for d.More() {
			k, e := d.Token()
			if e != nil {
				return e
			}
			key, ok := k.(string)
			if !ok || seen[key] {
				return fmt.Errorf("duplicate JSON key")
			}
			seen[key] = true
			if e = scan(d); e != nil {
				return e
			}
		}
	case '[':
		for d.More() {
			if e = scan(d); e != nil {
				return e
			}
		}
	default:
		return fmt.Errorf("invalid JSON")
	}
	_, e = d.Token()
	return e
}
func Names(project, stack string) error {
	if e := tokens.ValidateProjectName(project); e != nil {
		return e
	}
	_, e := tokens.ParseStackName(stack)
	return e
}
func Empty() []byte {
	m := apitype.ManifestV1{Time: time.Now().UTC(), Version: Version}
	m.Magic = m.NewMagic()
	d := apitype.DeploymentV3{Manifest: m, Resources: []apitype.ResourceV3{}, PendingOperations: []apitype.OperationV2{}}
	b, _ := json.Marshal(d)
	raw, _ := json.Marshal(apitype.UntypedDeployment{Version: 3, Deployment: b})
	return raw
}
func Validate(raw []byte) error {
	var u apitype.UntypedDeployment
	if e := Decode(raw, &u); e != nil {
		return e
	}
	if u.Version != 3 || len(u.Features) > 0 {
		return fmt.Errorf("unsupported deployment schema/features")
	}
	var d map[string]any
	if e := Decode(u.Deployment, &d); e != nil {
		return e
	}
	if d == nil || d["manifest"] == nil {
		return fmt.Errorf("deployment manifest is required")
	}
	check := func(m map[string]any) error {
		for _, k := range []string{"snippets", "views", "hooks", "taint", "replaceWith", "refreshBeforeUpdate"} {
			if _, ok := m[k]; ok {
				return fmt.Errorf("unsupported v4 feature: %s", k)
			}
		}
		return nil
	}
	if e := check(d); e != nil {
		return e
	}
	for _, field := range []string{"resources", "pending_operations"} {
		if a, ok := d[field].([]any); ok {
			for _, v := range a {
				m, ok := v.(map[string]any)
				if !ok {
					return fmt.Errorf("invalid resource")
				}
				if field == "pending_operations" {
					m, ok = m["resource"].(map[string]any)
					if !ok {
						return fmt.Errorf("invalid pending resource")
					}
				}
				if e := check(m); e != nil {
					return e
				}
			}
		}
	}
	var typed apitype.DeploymentV3
	if e := json.Unmarshal(u.Deployment, &typed); e != nil {
		return e
	}
	for _, r := range typed.Resources {
		if !r.URN.IsValid() || r.Type == "" {
			return fmt.Errorf("resource requires a valid URN and type")
		}
	}
	for _, op := range typed.PendingOperations {
		if !op.Resource.URN.IsValid() || op.Resource.Type == "" {
			return fmt.Errorf("pending resource requires a valid URN and type")
		}
	}
	return nil
}
func ResourceCount(raw []byte) int {
	var u apitype.UntypedDeployment
	var d apitype.DeploymentV3
	json.Unmarshal(raw, &u)
	json.Unmarshal(u.Deployment, &d)
	return len(d.Resources)
}
func ContainsResources(raw []byte) bool {
	var u apitype.UntypedDeployment
	var d apitype.DeploymentV3
	json.Unmarshal(raw, &u)
	json.Unmarshal(u.Deployment, &d)
	if len(d.PendingOperations) > 0 {
		return true
	}
	for _, r := range d.Resources {
		if r.Type != tokens.Type("pulumi:pulumi:Stack") {
			return true
		}
	}
	return false
}

const sigKey = "4dabf18193072939515e22adb298388d"
const secretSig = "1b47061264138c4ac30d75fd1eb44270"
const refSig = "5cf8f73096256a8f31e491e813e4eb8e"

func Redact(v any) any {
	switch x := v.(type) {
	case map[string]any:
		if x[sigKey] == secretSig || x["secret"] == true {
			return "[secret]"
		}
		y := map[string]any{}
		for k, v := range x {
			y[k] = Redact(v)
		}
		if a, ok := x["additionalSecretOutputs"].([]any); ok {
			if o, ok := y["outputs"].(map[string]any); ok {
				for _, k := range a {
					if s, ok := k.(string); ok {
						o[s] = "[secret]"
					}
				}
			}
		}
		return y
	case []any:
		y := make([]any, len(x))
		for i, v := range x {
			y[i] = Redact(v)
		}
		return y
	}
	return v
}

// Rename changes structured resource identity only, never application strings or ciphertexts.
func Rename(raw []byte, org, project, name, newProject, newName, publicURL string) ([]byte, error) {
	var u map[string]any
	if e := Decode(raw, &u); e != nil {
		return nil, e
	}
	d := u["deployment"].(map[string]any)
	rewrite := func(s string) string {
		urn := resource.URN(s)
		if !urn.IsValid() || string(urn.Stack()) != name || string(urn.Project()) != project {
			return s
		}
		n := urn.Name()
		if urn.Type() == "pulumi:pulumi:Stack" {
			n = newProject + "-" + newName
		}
		return string(resource.NewURN(tokens.QName(newName), tokens.PackageName(newProject), "", urn.QualifiedType(), n))
	}
	var refs func(any)
	refs = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			if x[sigKey] == refSig {
				if s, ok := x["urn"].(string); ok {
					x["urn"] = rewrite(s)
				}
			}
			for _, v := range x {
				refs(v)
			}
		case []any:
			for _, v := range x {
				refs(v)
			}
		}
	}
	rewriteResource := func(r map[string]any) {
		for _, k := range []string{"urn", "parent", "deletedWith"} {
			if v, ok := r[k].(string); ok {
				r[k] = rewrite(v)
			}
		}
		if v, ok := r["provider"].(string); ok {
			for i := len(v) - 1; i >= 1; i-- {
				if v[i-1:i+1] == "::" {
					r["provider"] = rewrite(v[:i-1]) + v[i-1:]
					break
				}
			}
		}
		if a, ok := r["dependencies"].([]any); ok {
			for i, v := range a {
				if s, ok := v.(string); ok {
					a[i] = rewrite(s)
				}
			}
		}
		if m, ok := r["propertyDependencies"].(map[string]any); ok {
			for _, v := range m {
				if a, ok := v.([]any); ok {
					for i, v := range a {
						if s, ok := v.(string); ok {
							a[i] = rewrite(s)
						}
					}
				}
			}
		}
		refs(r["inputs"])
		refs(r["outputs"])
	}
	if a, ok := d["resources"].([]any); ok {
		for _, v := range a {
			rewriteResource(v.(map[string]any))
		}
	}
	if a, ok := d["pending_operations"].([]any); ok {
		for _, v := range a {
			if r, ok := v.(map[string]any)["resource"].(map[string]any); ok {
				rewriteResource(r)
			}
		}
	}
	if sp, ok := d["secrets_providers"].(map[string]any); ok && sp["type"] == "service" {
		if state, ok := sp["state"].(map[string]any); ok {
			state["url"] = publicURL
			state["owner"] = org
			state["project"] = newProject
			state["stack"] = newName
		}
	}
	return json.Marshal(u)
}
func ValidateJournal(raw []byte) error {
	var m map[string]json.RawMessage
	if e := Decode(raw, &m); e != nil {
		return e
	}
	allowed := map[string]bool{}
	for _, k := range []string{"version", "kind", "sequenceID", "operationID", "removeOld", "removeNew", "pendingReplacementOld", "pendingReplacementNew", "deleteOld", "deleteNew", "state", "operation", "isRefresh", "secretsProvider", "newSnapshot"} {
		allowed[k] = true
	}
	for k := range m {
		if !allowed[k] {
			return fmt.Errorf("unsupported journal field %s", k)
		}
	}
	for _, k := range []string{"version", "kind", "sequenceID", "operationID"} {
		if _, ok := m[k]; !ok {
			return fmt.Errorf("missing journal field %s", k)
		}
	}
	check := func(raw json.RawMessage) error {
		if len(raw) == 0 || string(raw) == "null" {
			return nil
		}
		var resource map[string]any
		if e := Decode(raw, &resource); e != nil {
			return e
		}
		for _, k := range []string{"snippets", "views", "hooks", "taint", "replaceWith", "refreshBeforeUpdate"} {
			if _, ok := resource[k]; ok {
				return fmt.Errorf("unsupported v4 journal field %s", k)
			}
		}
		return nil
	}
	if e := check(m["state"]); e != nil {
		return e
	}
	if b := m["operation"]; len(b) > 0 && string(b) != "null" {
		var op map[string]json.RawMessage
		if e := Decode(b, &op); e != nil {
			return e
		}
		if e := check(op["resource"]); e != nil {
			return e
		}
	}
	if b := m["newSnapshot"]; len(b) > 0 && string(b) != "null" {
		return Validate(append(append([]byte(`{"version":3,"deployment":`), b...), '}'))
	}
	return nil
}
