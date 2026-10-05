package core

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

type Cursor struct {
	Kind       string `json:"kind"`
	Principal  string `json:"principalID"`
	Scope      string `json:"scopeID"`
	Filter     string `json:"filterHash"`
	Position   string `json:"lastPosition"`
	Generation string `json:"generation"`
	Expires    int64  `json:"expiresAt"`
}

func EncodeCursor(key []byte, c Cursor) string {
	c.Expires = time.Now().Add(24 * time.Hour).Unix()
	b, _ := json.Marshal(c)
	h := hmac.New(sha256.New, key)
	h.Write(b)
	return base64.RawURLEncoding.EncodeToString(b) + "." + base64.RawURLEncoding.EncodeToString(h.Sum(nil))
}
func DecodeCursor(key []byte, s string, expected Cursor) (Cursor, error) {
	bad := errors.New("Invalid continuation token")
	parts := strings.Split(s, ".")
	if len(parts) != 2 {
		return Cursor{}, bad
	}
	b, e := base64.RawURLEncoding.DecodeString(parts[0])
	if e != nil {
		return Cursor{}, bad
	}
	sig, e := base64.RawURLEncoding.DecodeString(parts[1])
	if e != nil {
		return Cursor{}, bad
	}
	h := hmac.New(sha256.New, key)
	h.Write(b)
	if !hmac.Equal(h.Sum(nil), sig) {
		return Cursor{}, bad
	}
	var c Cursor
	if e = json.Unmarshal(b, &c); e != nil || c.Kind != expected.Kind || c.Principal != expected.Principal || c.Scope != expected.Scope || c.Filter != expected.Filter || c.Generation != expected.Generation || c.Expires < time.Now().Unix() {
		return Cursor{}, bad
	}
	return c, nil
}
