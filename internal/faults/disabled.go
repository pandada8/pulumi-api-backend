//go:build !faulttest

package faults

import (
	"context"
	"net/http"
	"strings"
)

type SkipKey struct{}

func Handle(w http.ResponseWriter, r *http.Request) bool {
	if strings.HasPrefix(r.URL.Path, "/_test/") {
		http.NotFound(w, r)
		return true
	}
	return false
}
func Hit(context.Context, string) error { return nil }
