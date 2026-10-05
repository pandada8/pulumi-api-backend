package contract

import (
	"context"
	"github.com/pulumi/pulumi/pkg/v3/backend/httpstate/client"
	"github.com/pulumi/pulumi/sdk/v3/go/common/diag"
	"github.com/pulumi/pulumi/sdk/v3/go/common/diag/colors"
	"os"
	"strings"
	"testing"
)

func TestUpstreamClient(t *testing.T) {
	url := os.Getenv("TEST_API_URL")
	file := os.Getenv("TEST_TOKEN_FILE")
	if url == "" || file == "" {
		t.Skip("isolated test harness supplies TEST_API_URL and TEST_TOKEN_FILE")
	}
	b, e := os.ReadFile(file)
	if e != nil {
		t.Fatal(e)
	}
	c := client.NewClient(url, strings.TrimSpace(string(b)), false, diag.DefaultSink(os.Stdout, os.Stderr, diag.FormatOptions{Color: colors.Never}))
	ctx := context.Background()
	login, orgs, _, e := c.GetPulumiAccountDetails(ctx)
	if e != nil || login != "admin" || len(orgs) != 1 || orgs[0] != "demo" {
		t.Fatal(login, orgs, e)
	}
	latest, oldest, dev, e := c.GetCLIVersionInfo(ctx, nil)
	if e != nil || latest.String() != "3.246.0" || oldest.String() != latest.String() || dev.String() != latest.String() {
		t.Fatal("CLI version contract", e)
	}
	cap, e := c.GetCapabilities(ctx)
	if e != nil || len(cap.Capabilities) != 1 {
		t.Fatal("capability contract", e)
	}
}
