package httpapi

import (
	"io/fs"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestConsoleShellAndAssets(t *testing.T) {
	a := &API{}
	shell := httptest.NewRecorder()
	a.ServeHTTP(shell, httptest.NewRequest("GET", "/login", nil))
	if shell.Code != 200 || !strings.Contains(shell.Body.String(), `id="root"`) {
		t.Fatalf("console shell: %d", shell.Code)
	}
	if strings.Contains(shell.Header().Get("Content-Security-Policy"), "script-src 'unsafe-inline'") {
		t.Fatal("inline scripts permitted")
	}
	if shell.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("shell must not be cached across deployments")
	}
	files, err := fs.ReadDir(consoleFS, "assets")
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		path := "/assets/" + file.Name()
		asset := httptest.NewRecorder()
		a.ServeHTTP(asset, httptest.NewRequest("GET", path, nil))
		if asset.Code != 200 || !strings.Contains(asset.Header().Get("Cache-Control"), "immutable") || asset.Body.Len() == 0 {
			t.Fatal("missing embedded asset", path)
		}
	}
	missing := httptest.NewRecorder()
	a.ServeHTTP(missing, httptest.NewRequest("GET", "/assets/missing.js", nil))
	if missing.Code != 404 || strings.Contains(missing.Header().Get("Cache-Control"), "immutable") {
		t.Fatal("missing asset was cached or served as HTML")
	}
	mutation := httptest.NewRecorder()
	a.ServeHTTP(mutation, httptest.NewRequest("POST", "/assets/missing.js", nil))
	if mutation.Code != 405 {
		t.Fatal("asset accepts mutations")
	}
}
