package httpapi

import (
	"embed"
	"encoding/json"
	"io/fs"
	"net/http"
	"strings"

	"github.com/pandada8/pulumi-api-backend/internal/pulumicompat"
)

// The checked-in production assets keep plain go build usable without Node.
// Regenerate with make web-build; Docker builds them from source.
//
//go:embed console/dist
var consoleFiles embed.FS

var consoleFS, _ = fs.Sub(consoleFiles, "console/dist")
var consoleAssets = http.FileServer(http.FS(consoleFS))

func (a *API) consoleAsset(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" && r.Method != "HEAD" {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	entry, err := fs.Stat(consoleFS, strings.TrimPrefix(r.URL.Path, "/"))
	if err != nil || entry.IsDir() {
		http.NotFound(w, r)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/assets/") {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	}
	consoleAssets.ServeHTTP(w, r)
}
func (a *API) consoleShell(w http.ResponseWriter, r *http.Request) {
	b, e := consoleFiles.ReadFile("console/dist/index.html")
	if e != nil {
		http.Error(w, "Console assets unavailable", 500)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if r.Method != "HEAD" {
		w.Write(b)
	}
}
func redactConsole(value any) any {
	b, _ := json.Marshal(value)
	var generic any
	if pulumicompat.Decode(b, &generic) != nil {
		return nil
	}
	return pulumicompat.Redact(generic)
}
