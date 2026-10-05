package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/pulumi/pulumi/pkg/v3/resource/provider"
	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
	"github.com/pulumi/pulumi/sdk/v3/go/common/resource/plugin"
	"github.com/pulumi/pulumi/sdk/v3/go/common/util/cmdutil"
	rpc "github.com/pulumi/pulumi/sdk/v3/proto/go"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/structpb"
	"io"
	"net/http"
	"net/url"
	"strings"
)

type testProvider struct {
	rpc.UnimplementedResourceProviderServer
	URL string
}

func main() {
	if e := provider.Main("backendtest", func(*provider.HostClient) (rpc.ResourceProviderServer, error) { return &testProvider{}, nil }); e != nil {
		cmdutil.Exit(e)
	}
}
func (p *testProvider) GetPluginInfo(context.Context, *emptypb.Empty) (*rpc.PluginInfo, error) {
	return &rpc.PluginInfo{Version: "0.0.1"}, nil
}
func (p *testProvider) GetSchema(context.Context, *rpc.GetSchemaRequest) (*rpc.GetSchemaResponse, error) {
	properties := map[string]any{}
	for _, k := range []string{"name", "value", "replaceKey", "secretValue"} {
		properties[k] = map[string]any{"type": "string"}
	}
	schema := map[string]any{"name": "backendtest", "version": "0.0.1", "provider": map[string]any{"inputProperties": map[string]any{"worldURL": map[string]any{"type": "string"}}, "requiredInputs": []string{"worldURL"}}, "resources": map[string]any{"backendtest:index:Item": map[string]any{"inputProperties": properties, "properties": properties, "requiredInputs": []string{"name", "value", "replaceKey"}, "required": []string{"name", "value", "replaceKey"}}}}
	b, _ := json.Marshal(schema)
	return &rpc.GetSchemaResponse{Schema: string(b)}, nil
}
func worldURL(s *structpb.Struct) (string, error) {
	m, e := plugin.UnmarshalProperties(s, plugin.MarshalOptions{KeepSecrets: true, KeepUnknowns: true})
	if e != nil {
		return "", e
	}
	v, ok := m["worldURL"]
	if !ok || !v.IsString() {
		return "", fmt.Errorf("worldURL required")
	}
	u, e := url.Parse(v.StringValue())
	if e != nil || u.Scheme != "http" || (u.Hostname() != "127.0.0.1" && u.Hostname() != "localhost") {
		return "", fmt.Errorf("worldURL must be a loopback HTTP URL")
	}
	return strings.TrimRight(v.StringValue(), "/"), nil
}
func (p *testProvider) CheckConfig(ctx context.Context, r *rpc.CheckRequest) (*rpc.CheckResponse, error) {
	if _, e := worldURL(r.News); e != nil {
		return &rpc.CheckResponse{Inputs: r.News, Failures: []*rpc.CheckFailure{{Property: "worldURL", Reason: e.Error()}}}, nil
	}
	return &rpc.CheckResponse{Inputs: r.News}, nil
}
func (p *testProvider) DiffConfig(ctx context.Context, r *rpc.DiffRequest) (*rpc.DiffResponse, error) {
	a, _ := worldURL(r.Olds)
	b, _ := worldURL(r.News)
	if a != b {
		return &rpc.DiffResponse{Changes: rpc.DiffResponse_DIFF_SOME, Replaces: []string{"worldURL"}}, nil
	}
	return &rpc.DiffResponse{Changes: rpc.DiffResponse_DIFF_NONE}, nil
}
func (p *testProvider) Configure(ctx context.Context, r *rpc.ConfigureRequest) (*rpc.ConfigureResponse, error) {
	u, e := worldURL(r.Args)
	if e != nil {
		return nil, e
	}
	p.URL = u
	return &rpc.ConfigureResponse{AcceptSecrets: true, SupportsPreview: true}, nil
}
func (p *testProvider) Check(ctx context.Context, r *rpc.CheckRequest) (*rpc.CheckResponse, error) {
	m, e := plugin.UnmarshalProperties(r.News, plugin.MarshalOptions{KeepSecrets: true, KeepUnknowns: true})
	if e != nil {
		return nil, e
	}
	fail := []*rpc.CheckFailure{}
	for _, k := range []string{"name", "value", "replaceKey"} {
		if _, ok := m[resource.PropertyKey(k)]; !ok {
			fail = append(fail, &rpc.CheckFailure{Property: k, Reason: "required"})
		}
	}
	return &rpc.CheckResponse{Inputs: r.News, Failures: fail}, nil
}
func (p *testProvider) Diff(ctx context.Context, r *rpc.DiffRequest) (*rpc.DiffResponse, error) {
	old, new := r.Olds.AsMap(), r.News.AsMap()
	a, _ := json.Marshal(old)
	b, _ := json.Marshal(new)
	out := &rpc.DiffResponse{Changes: rpc.DiffResponse_DIFF_NONE}
	if !bytes.Equal(a, b) {
		out.Changes = rpc.DiffResponse_DIFF_SOME
	}
	if old["replaceKey"] != new["replaceKey"] {
		out.Replaces = []string{"replaceKey"}
	}
	return out, nil
}

type item struct {
	ID     string           `json:"id"`
	Inputs *structpb.Struct `json:"inputs"`
}

func (p *testProvider) call(ctx context.Context, method, path string, in any) (item, error) {
	var body io.Reader
	if in != nil {
		b, e := json.Marshal(in)
		if e != nil {
			return item{}, e
		}
		body = bytes.NewReader(b)
	}
	r, e := http.NewRequestWithContext(ctx, method, p.URL+path, body)
	if e != nil {
		return item{}, e
	}
	r.Header.Set("Content-Type", "application/json")
	res, e := http.DefaultClient.Do(r)
	if e != nil {
		return item{}, e
	}
	defer res.Body.Close()
	if res.StatusCode == 404 {
		return item{}, nil
	}
	if res.StatusCode >= 300 {
		return item{}, fmt.Errorf("world operation failed: %d", res.StatusCode)
	}
	var out item
	if res.StatusCode != 204 {
		e = json.NewDecoder(res.Body).Decode(&out)
	}
	return out, e
}
func (p *testProvider) Create(ctx context.Context, r *rpc.CreateRequest) (*rpc.CreateResponse, error) {
	if r.Preview {
		return &rpc.CreateResponse{Properties: r.Properties}, nil
	}
	v, e := p.call(ctx, "POST", "/resources", item{Inputs: r.Properties})
	return &rpc.CreateResponse{Id: v.ID, Properties: v.Inputs}, e
}
func (p *testProvider) Read(ctx context.Context, r *rpc.ReadRequest) (*rpc.ReadResponse, error) {
	v, e := p.call(ctx, "GET", "/resources/"+url.PathEscape(r.Id), nil)
	return &rpc.ReadResponse{Id: v.ID, Inputs: v.Inputs, Properties: v.Inputs}, e
}
func (p *testProvider) Update(ctx context.Context, r *rpc.UpdateRequest) (*rpc.UpdateResponse, error) {
	if r.Preview {
		return &rpc.UpdateResponse{Properties: r.News}, nil
	}
	v, e := p.call(ctx, "PUT", "/resources/"+url.PathEscape(r.Id), item{ID: r.Id, Inputs: r.News})
	return &rpc.UpdateResponse{Properties: v.Inputs}, e
}
func (p *testProvider) Delete(ctx context.Context, r *rpc.DeleteRequest) (*emptypb.Empty, error) {
	_, e := p.call(ctx, "DELETE", "/resources/"+url.PathEscape(r.Id), nil)
	return &emptypb.Empty{}, e
}
