// world is a loopback-only test resource service. Never deploy it as a product endpoint.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"github.com/google/uuid"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type world struct {
	mu         sync.Mutex
	Dir        string
	Control    string
	Delay      int
	FailDelete bool
}

func (w *world) syncDir() error {
	f, e := os.Open(w.Dir)
	if e != nil {
		return e
	}
	defer f.Close()
	return f.Sync()
}
func (w *world) ServeHTTP(out http.ResponseWriter, r *http.Request) {
	w.mu.Lock()
	defer w.mu.Unlock()
	out.Header().Set("Content-Type", "application/json")
	if r.URL.Path == "/control" {
		if r.Header.Get("X-Test-Control") != w.Control || w.Control == "" {
			http.Error(out, "Forbidden", 403)
			return
		}
		var c struct {
			Delay      int  `json:"delayMs"`
			FailDelete bool `json:"failDelete"`
		}
		if e := json.NewDecoder(r.Body).Decode(&c); e != nil {
			http.Error(out, "Invalid request", 400)
			return
		}
		w.Delay = c.Delay
		w.FailDelete = c.FailDelete
		json.NewEncoder(out).Encode(map[string]any{"ok": true})
		return
	}
	if r.URL.Path == "/resources" && r.Method == "GET" {
		entries, e := os.ReadDir(w.Dir)
		if e != nil {
			http.Error(out, "read failed", 500)
			return
		}
		items := []json.RawMessage{}
		for _, entry := range entries {
			if strings.HasSuffix(entry.Name(), ".json") {
				b, e := os.ReadFile(filepath.Join(w.Dir, entry.Name()))
				if e != nil {
					http.Error(out, "read failed", 500)
					return
				}
				items = append(items, b)
			}
		}
		json.NewEncoder(out).Encode(items)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/resources/")
	if r.URL.Path == "/resources" && r.Method == "POST" {
		id = uuid.NewString()
	}
	if _, e := uuid.Parse(id); e != nil {
		http.NotFound(out, r)
		return
	}
	path := filepath.Join(w.Dir, id+".json")
	switch r.Method {
	case "GET":
		b, e := os.ReadFile(path)
		if os.IsNotExist(e) {
			http.NotFound(out, r)
			return
		}
		if e != nil {
			http.Error(out, "read failed", 500)
			return
		}
		out.Write(b)
	case "DELETE":
		if w.FailDelete {
			http.Error(out, "injected delete failure", 500)
			return
		}
		e := os.Remove(path)
		if e != nil && !os.IsNotExist(e) {
			http.Error(out, "delete failed", 500)
			return
		}
		if e = w.syncDir(); e != nil {
			http.Error(out, "sync failed", 500)
			return
		}
		out.WriteHeader(204)
	case "PUT", "POST":
		var value map[string]any
		if e := json.NewDecoder(r.Body).Decode(&value); e != nil {
			http.Error(out, "invalid JSON", 400)
			return
		}
		value["id"] = id
		b, e := json.Marshal(value)
		if e != nil {
			http.Error(out, "invalid resource", 400)
			return
		}
		f, e := os.CreateTemp(w.Dir, ".resource-")
		if e != nil {
			http.Error(out, "write failed", 500)
			return
		}
		tmp := f.Name()
		defer os.Remove(tmp)
		if _, e = f.Write(b); e == nil {
			e = f.Sync()
		}
		ce := f.Close()
		if e == nil {
			e = ce
		}
		if e == nil {
			e = os.Rename(tmp, path)
		}
		if e == nil {
			e = w.syncDir()
		}
		if e != nil {
			http.Error(out, "persist failed", 500)
			return
		}
		delay := w.Delay
		w.mu.Unlock()
		select {
		case <-r.Context().Done():
		case <-time.After(time.Duration(delay) * time.Millisecond):
			out.Write(b)
		}
		w.mu.Lock()
	default:
		http.NotFound(out, r)
	}
}
func main() {
	dir := flag.String("dir", ".dev/world", "")
	listen := flag.String("listen", "127.0.0.1:7071", "")
	flag.Parse()
	if !strings.HasPrefix(*listen, "127.0.0.1:") {
		panic("world must bind to loopback")
	}
	if e := os.MkdirAll(*dir, 0700); e != nil {
		panic(e)
	}
	fmt.Fprintln(os.Stderr, "test world listening", *listen)
	if e := http.ListenAndServe(*listen, &world{Dir: *dir, Control: os.Getenv("WORLD_CONTROL_TOKEN")}); e != nil {
		panic(e)
	}
}
