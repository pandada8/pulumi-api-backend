//go:build faulttest

package faults

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"
	"sync"
)

type gate struct {
	mode    string
	release chan struct{}
	hit     int
}

var mu sync.Mutex
var gates = map[string]*gate{}
var ErrDrop = errors.New("test: drop response")

type SkipKey struct{}

func Hit(ctx context.Context, name string) error {
	if skip, _ := ctx.Value(SkipKey{}).(bool); skip {
		return nil
	}
	if os.Getenv("BACKEND_TEST_FAULTS") != "true" {
		return nil
	}
	mu.Lock()
	g := gates[name]
	if g == nil {
		mu.Unlock()
		return nil
	}
	g.hit++
	mode, release := g.mode, g.release
	if mode == "drop" {
		delete(gates, name)
	}
	mu.Unlock()
	switch mode {
	case "block":
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-release:
			return nil
		}
	case "drop":
		return ErrDrop
	case "fail":
		return errors.New("injected failure")
	}
	return nil
}
func Handle(w http.ResponseWriter, r *http.Request) bool {
	if !strings.HasPrefix(r.URL.Path, "/_test/faults") {
		return false
	}
	if os.Getenv("BACKEND_TEST_FAULTS") != "true" {
		http.NotFound(w, r)
		return true
	}
	expected := os.Getenv("BACKEND_TEST_CONTROL_TOKEN")
	token := r.Header.Get("X-Test-Control")
	if expected == "" || subtle.ConstantTimeCompare([]byte(expected), []byte(token)) != 1 {
		http.Error(w, "Forbidden", 403)
		return true
	}
	name := strings.TrimPrefix(r.URL.Path, "/_test/faults/")
	mu.Lock()
	defer mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	if r.Method == "GET" {
		hits := 0
		if g := gates[name]; g != nil {
			hits = g.hit
		}
		json.NewEncoder(w).Encode(map[string]any{"hits": hits})
		return true
	}
	if r.Method == "DELETE" {
		if g := gates[name]; g != nil && g.mode == "block" {
			close(g.release)
		}
		delete(gates, name)
		json.NewEncoder(w).Encode(map[string]any{"released": true})
		return true
	}
	var req struct {
		Mode string `json:"mode"`
	}
	if e := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&req); e != nil {
		http.Error(w, "Bad request", 400)
		return true
	}
	if req.Mode != "block" && req.Mode != "drop" && req.Mode != "fail" {
		http.Error(w, "Bad mode", 400)
		return true
	}
	if g := gates[name]; g != nil && g.mode == "block" {
		close(g.release)
	}
	gates[name] = &gate{mode: req.Mode, release: make(chan struct{})}
	json.NewEncoder(w).Encode(map[string]any{"armed": true})
	return true
}
