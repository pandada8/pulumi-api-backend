package pulumicompat

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
)

type ResourceDiff struct {
	URN    string   `json:"urn"`
	ID     string   `json:"id"`
	Action string   `json:"action"`
	Paths  []string `json:"paths,omitempty"`
}

// Diff compares identity and property paths. It never returns property values or ciphertexts.
func Diff(before, after []byte) ([]ResourceDiff, error) {
	parse := func(raw []byte) (map[string]map[string]any, error) {
		var e map[string]any
		if len(raw) == 0 {
			return map[string]map[string]any{}, nil
		}
		if err := Decode(raw, &e); err != nil {
			return nil, err
		}
		d, _ := e["deployment"].(map[string]any)
		out := map[string]map[string]any{}
		resources, _ := d["resources"].([]any)
		for _, v := range resources {
			r, ok := v.(map[string]any)
			if !ok {
				continue
			}
			urn, ok := r["urn"].(string)
			if !ok {
				return nil, fmt.Errorf("resource URN missing")
			}
			key := urn + "\x00"
			if id, ok := r["id"].(string); ok {
				key += id
			}
			if r["delete"] == true {
				key += "\x00deleted"
			}
			out[key] = r
		}
		return out, nil
	}
	a, e := parse(before)
	if e != nil {
		return nil, e
	}
	b, e := parse(after)
	if e != nil {
		return nil, e
	}
	keys := map[string]bool{}
	for k := range a {
		keys[k] = true
	}
	for k := range b {
		keys[k] = true
	}
	order := []string{}
	for k := range keys {
		order = append(order, k)
	}
	sort.Strings(order)
	result := []ResourceDiff{}
	for _, k := range order {
		x, xok := a[k]
		y, yok := b[k]
		resource := y
		if !yok {
			resource = x
		}
		urn, _ := resource["urn"].(string)
		id, _ := resource["id"].(string)
		diff := ResourceDiff{URN: urn, ID: id, Action: "same"}
		switch {
		case !xok:
			diff.Action = "create"
		case !yok:
			diff.Action = "delete"
		case !reflect.DeepEqual(x, y):
			diff.Action = "update"
			var walk func(string, any, any)
			walk = func(path string, a, b any) {
				if reflect.DeepEqual(a, b) {
					return
				}
				am, aok := a.(map[string]any)
				bm, bok := b.(map[string]any)
				if aok && bok && am[sigKey] != secretSig && bm[sigKey] != secretSig {
					keys := map[string]bool{}
					for k := range am {
						keys[k] = true
					}
					for k := range bm {
						keys[k] = true
					}
					for k := range keys {
						child := path + "/" + strings.ReplaceAll(strings.ReplaceAll(k, "~", "~0"), "/", "~1")
						av, aexists := am[k]
						bv, bexists := bm[k]
						if aexists != bexists {
							diff.Paths = append(diff.Paths, child)
						} else {
							walk(child, av, bv)
						}
					}
				} else {
					diff.Paths = append(diff.Paths, path)
				}
			}
			walk("", x, y)
			sort.Strings(diff.Paths)
		}
		result = append(result, diff)
	}
	return result, nil
}
