package httpapi

import (
	"bytes"
	"net/http/httptest"
	"testing"
)

func TestLargeStateBodyLimits(t *testing.T) {
	// An incompressible/encrypted state may exceed the former 32 MiB wire
	// limit. Exercise both upload routes and the unchanged small-route bound.
	raw := append([]byte(`{"data":"`), bytes.Repeat([]byte("x"), 33<<20)...)
	raw = append(raw, []byte(`"}`)...)
	for _, path := range []string{"/api/stacks/org/project/stack/import", "/api/stacks/org/project/stack/update/id/checkpoint"} {
		r := httptest.NewRequest("POST", path, bytes.NewReader(raw))
		got, err := body(httptest.NewRecorder(), r)
		if err != nil || !bytes.Equal(got, raw) {
			t.Fatalf("%s: large state rejected: %v", path, err)
		}
	}
	r := httptest.NewRequest("POST", "/api/stacks/org/project", bytes.NewReader(raw))
	if _, err := body(httptest.NewRecorder(), r); err == nil {
		t.Fatal("large non-state request accepted")
	}
}
