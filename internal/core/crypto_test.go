package core

import (
	"bytes"
	"testing"
	"time"
)

func TestEnvelope(t *testing.T) {
	key := Random()
	plain := []byte("secret-canary")
	a, e := Encrypt(key, "stack-a", 1, plain)
	if e != nil {
		t.Fatal(e)
	}
	b, e := Encrypt(key, "stack-a", 1, plain)
	if e != nil || bytes.Equal(a, b) {
		t.Fatal("nonce reuse")
	}
	v, e := Decrypt(key, "stack-a", a)
	if e != nil || !bytes.Equal(v, plain) {
		t.Fatal("round trip", e)
	}
	if _, e = Decrypt(key, "stack-b", a); e == nil {
		t.Fatal("cross-stack decrypt")
	}
	for i := range a {
		bad := append([]byte{}, a...)
		bad[i] ^= 1
		if _, e := Decrypt(key, "stack-a", bad); e == nil {
			t.Fatalf("tamper accepted byte %d", i)
		}
	}
	if bytes.Equal(Purpose(key, "wrap"), Purpose(key, "cursor")) {
		t.Fatal("purpose reuse")
	}
}
func TestCursor(t *testing.T) {
	key := Random()
	expected := Cursor{Kind: "events", Principal: "user", Scope: "stack", Filter: "filter", Generation: "gen", Position: "123"}
	s := EncodeCursor(key, expected)
	c, e := DecodeCursor(key, s, expected)
	if e != nil || c.Position != "123" {
		t.Fatal(e)
	}
	for _, field := range []string{"kind", "principal", "scope", "filter", "generation"} {
		bad := expected
		switch field {
		case "kind":
			bad.Kind = "stacks"
		case "principal":
			bad.Principal = "another"
		case "scope":
			bad.Scope = "other"
		case "filter":
			bad.Filter = "other"
		case "generation":
			bad.Generation = "new"
		}
		if _, e = DecodeCursor(key, s, bad); e == nil {
			t.Fatal(field)
		}
	}
	if _, e = DecodeCursor(key, s+"x", expected); e == nil {
		t.Fatal("tamper")
	}
	_ = time.Now()
}
