package store

import (
	"encoding/json"
	"fmt"
	"github.com/pandada8/pulumi-api-backend/internal/pulumicompat"
	"github.com/pulumi/pulumi/sdk/v3/go/common/apitype"
	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
	"github.com/pulumi/pulumi/sdk/v3/go/common/tokens"
	"strings"
	"testing"
)

func TestJournalReplayRecovery(t *testing.T) {
	r := apitype.ResourceV3{URN: resource.URN("urn:pulumi:dev::wire::backendtest:index:Item::r"), Type: tokens.Type("backendtest:index:Item"), Custom: true, ID: resource.ID("world-r"), Outputs: map[string]any{"value": "v1"}}
	entries := []apitype.JournalEntry{{Version: 1, Kind: 0, SequenceID: 12, OperationID: 1, Operation: &apitype.OperationV2{Type: apitype.OperationTypeCreating, Resource: r}}}
	pending, e := replay(pulumicompat.Empty(), entries)
	if e != nil {
		t.Fatal(e)
	}
	var u apitype.UntypedDeployment
	var d apitype.DeploymentV3
	json.Unmarshal(pending, &u)
	json.Unmarshal(u.Deployment, &d)
	if len(d.PendingOperations) != 1 || len(d.Resources) != 0 {
		t.Fatal("lost pending create")
	}
	entries = append(entries, apitype.JournalEntry{Version: 1, Kind: 1, SequenceID: 11, OperationID: 1, State: &r})
	idx := int64(1)
	updated := r
	updated.Outputs = map[string]any{"value": "v2"}
	entries = append(entries, apitype.JournalEntry{Version: 1, Kind: 4, SequenceID: 13, OperationID: 2, RemoveNew: &idx, State: &updated})
	out, e := replay(pulumicompat.Empty(), entries)
	if e != nil {
		t.Fatal(e)
	}
	d = apitype.DeploymentV3{}
	json.Unmarshal(out, &u)
	json.Unmarshal(u.Deployment, &d)
	if len(d.Resources) != 1 || d.Resources[0].Outputs["value"] != "v2" || len(d.PendingOperations) != 0 {
		t.Fatal("outputs/update merge")
	}
	if _, e = replay(pulumicompat.Empty(), []apitype.JournalEntry{{Version: 1, Kind: 6, SequenceID: 1}}); e == nil {
		t.Fatal("missing secrets provider accepted")
	}
	bad := int64(42)
	if _, e = replay(pulumicompat.Empty(), []apitype.JournalEntry{{Version: 1, Kind: 4, SequenceID: 1, RemoveNew: &bad, State: &r}}); e == nil {
		t.Fatal("invalid reference accepted")
	}
}
func TestJournalBeginMayOmitOperation(t *testing.T) {
	v := newValidator(0)
	if e := v.add(apitype.JournalEntry{Version: 1, Kind: 0, SequenceID: 1, OperationID: 1}); e != nil {
		t.Fatal(e)
	}
	if e := v.add(apitype.JournalEntry{Version: 1, Kind: 2, SequenceID: 2, OperationID: 1}); e != nil {
		t.Fatal(e)
	}
}
func BenchmarkReplay(b *testing.B) {
	for _, n := range []int{1000, 10000} {
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			var envelope apitype.UntypedDeployment
			var deployment apitype.DeploymentV3
			json.Unmarshal(pulumicompat.Empty(), &envelope)
			json.Unmarshal(envelope.Deployment, &deployment)
			for i := 0; i < n; i++ {
				deployment.Resources = append(deployment.Resources, apitype.ResourceV3{URN: resource.URN(fmt.Sprintf("urn:pulumi:dev::wire::backendtest:index:Item::r%d", i)), Type: tokens.Type("backendtest:index:Item"), Custom: true, ID: resource.ID(fmt.Sprint(i)), Inputs: map[string]any{"value": strings.Repeat("x", 1024)}})
			}
			raw, _ := json.Marshal(deployment)
			base, _ := json.Marshal(apitype.UntypedDeployment{Version: 3, Deployment: raw})
			entries := []apitype.JournalEntry{}
			for i := 0; i < n/100; i++ {
				r := deployment.Resources[i]
				r.Outputs = map[string]any{"value": "changed"}
				index := int64(i)
				entries = append(entries, apitype.JournalEntry{Version: 1, Kind: 0, SequenceID: int64(2*i + 1), OperationID: int64(i + 1)}, apitype.JournalEntry{Version: 1, Kind: 1, SequenceID: int64(2*i + 2), OperationID: int64(i + 1), RemoveOld: &index, State: &r})
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, e := replay(base, entries); e != nil {
					b.Fatal(e)
				}
			}
		})
	}
}

func TestJournalRefreshRebuildContinues(t *testing.T) {
	r := apitype.ResourceV3{URN: resource.URN("urn:pulumi:dev::wire::backendtest:index:Item::r"), Type: tokens.Type("backendtest:index:Item")}
	var envelope apitype.UntypedDeployment
	json.Unmarshal(pulumicompat.Empty(), &envelope)
	var base apitype.DeploymentV3
	json.Unmarshal(envelope.Deployment, &base)
	base.Resources = []apitype.ResourceV3{r}
	envelope.Deployment, _ = json.Marshal(base)
	raw, _ := json.Marshal(envelope)
	old := int64(0)
	entries := []apitype.JournalEntry{
		{Version: 1, Kind: 0, SequenceID: 1, OperationID: 1},
		{Version: 1, Kind: 3, SequenceID: 2, OperationID: 1, RemoveOld: &old},
		{Version: 1, Kind: 7, SequenceID: 3},
		{Version: 1, Kind: 0, SequenceID: 4, OperationID: 2},
		{Version: 1, Kind: 1, SequenceID: 5, OperationID: 2, State: &r},
	}
	if _, e := replay(raw, entries); e != nil {
		t.Fatal(e)
	}
	// Refresh removed the original resource: its pre-rebuild index is invalid.
	invalid := append([]apitype.JournalEntry{}, entries[:3]...)
	invalid = append(invalid, apitype.JournalEntry{Version: 1, Kind: 4, SequenceID: 6, State: &r, RemoveOld: &old})
	if _, e := replay(raw, invalid); e == nil {
		t.Fatal("stale base index accepted")
	}
}

func TestReplayV4FeaturesAndSnippets(t *testing.T) {
	const urn = resource.URN("urn:pulumi:dev::wire::pkg:index:Thing::r")
	r := apitype.ResourceV3{URN: urn, Type: "pkg:index:Thing", ViewOf: urn, ReplaceWith: []resource.URN{urn}, Taint: true, RefreshBeforeUpdate: true, ResourceHooks: map[resource.HookType][]string{"beforeCreate": {"hook"}}}
	snippet := apitype.SnippetV1{Name: "s", Type: "pkg:index:Thing", Code: "value = 42", References: map[string]string{"r": string(urn)}}
	var base apitype.UntypedDeployment
	json.Unmarshal(pulumicompat.Empty(), &base)
	var deployment apitype.DeploymentV3
	json.Unmarshal(base.Deployment, &deployment)
	deployment.Snippets = []apitype.SnippetV1{snippet}
	base.Version, base.Features = 4, []string{"snippets-prototype"}
	base.Deployment, _ = json.Marshal(deployment)
	raw, _ := json.Marshal(base)
	entries := []apitype.JournalEntry{{Version: 1, Kind: 0, SequenceID: 1, OperationID: 1, Operation: &apitype.OperationV2{Type: apitype.OperationTypeCreating, Resource: r}}, {Version: 1, Kind: 1, SequenceID: 2, OperationID: 1, State: &r}, {Version: 1, Kind: 7, SequenceID: 3}}
	for _, entries := range [][]apitype.JournalEntry{entries[:1], entries, {{Version: 1, Kind: 5, SequenceID: 1, NewSnapshot: &apitype.DeploymentV3{Manifest: deployment.Manifest, Snippets: []apitype.SnippetV1{snippet}, Resources: []apitype.ResourceV3{r}}}}} {
		for _, entry := range entries {
			b, _ := json.Marshal(entry)
			if e := pulumicompat.ValidateJournal(b); e != nil {
				t.Fatal(e)
			}
		}
		out, e := replay(raw, entries)
		if e != nil {
			t.Fatal(e)
		}
		var u apitype.UntypedDeployment
		json.Unmarshal(out, &u)
		var d apitype.DeploymentV3
		json.Unmarshal(u.Deployment, &d)
		if u.Version != 4 || len(u.Features) != 6 || len(d.Snippets) != 1 || d.Snippets[0].References["r"] != string(urn) {
			t.Fatalf("lost features or snippets: %s", out)
		}
		again, e := replay(raw, entries)
		if e != nil || string(out) != string(again) {
			t.Fatal("replay not deterministic", e)
		}
	}
	// A v3 base can acquire v4 features through journal entries.
	out, e := replay(pulumicompat.Empty(), entries[:2])
	if e != nil {
		t.Fatal(e)
	}
	var u apitype.UntypedDeployment
	json.Unmarshal(out, &u)
	if u.Version != 4 || len(u.Features) != 5 {
		t.Fatalf("v3 upgrade lost features: %s", out)
	}
}

func TestReplayWriteClearsSnippets(t *testing.T) {
	var u apitype.UntypedDeployment
	json.Unmarshal(pulumicompat.Empty(), &u)
	var d apitype.DeploymentV3
	json.Unmarshal(u.Deployment, &d)
	d.Snippets = []apitype.SnippetV1{{Name: "old"}}
	u.Version, u.Features = 4, []string{"snippets-prototype"}
	u.Deployment, _ = json.Marshal(d)
	raw, _ := json.Marshal(u)
	d.Snippets = nil
	out, err := replay(raw, []apitype.JournalEntry{{Version: 1, Kind: 5, SequenceID: 1, NewSnapshot: &d}})
	if err != nil {
		t.Fatal(err)
	}
	json.Unmarshal(out, &u)
	d = apitype.DeploymentV3{}
	json.Unmarshal(u.Deployment, &d)
	if u.Version != 4 || len(d.Snippets) != 0 {
		t.Fatal("write did not clear snippets", string(out))
	}
}
