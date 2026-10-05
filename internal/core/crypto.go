package core

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
)

func Digest(b []byte) []byte { h := sha256.Sum256(b); return h[:] }
func Random() []byte {
	b := make([]byte, 32)
	if _, e := rand.Read(b); e != nil {
		panic(e)
	}
	return b
}
func RandomToken() string { return base64.RawURLEncoding.EncodeToString(Random()) }
func Purpose(master []byte, p string) []byte {
	h := hmac.New(sha256.New, master)
	h.Write([]byte("pulumi-backend/v1/" + p))
	return h.Sum(nil)
}
func Seal(key, plain []byte, aad string) ([]byte, error) {
	b, e := aes.NewCipher(key)
	if e != nil {
		return nil, e
	}
	g, e := cipher.NewGCM(b)
	if e != nil {
		return nil, e
	}
	n := make([]byte, g.NonceSize())
	if _, e = rand.Read(n); e != nil {
		return nil, e
	}
	return g.Seal(n, n, plain, []byte(aad)), nil
}
func Open(key, raw []byte, aad string) ([]byte, error) {
	b, e := aes.NewCipher(key)
	if e != nil {
		return nil, e
	}
	g, e := cipher.NewGCM(b)
	if e != nil {
		return nil, e
	}
	if len(raw) < g.NonceSize()+g.Overhead() {
		return nil, errors.New("Invalid ciphertext")
	}
	v, e := g.Open(nil, raw[:g.NonceSize()], raw[g.NonceSize():], []byte(aad))
	if e != nil {
		return nil, errors.New("Invalid ciphertext")
	}
	return v, nil
}
func Encrypt(key []byte, stack string, version uint32, plain []byte) ([]byte, error) {
	raw, e := Seal(key, plain, fmt.Sprintf("pb1:stack:%s:key:%d", stack, version))
	if e != nil {
		return nil, e
	}
	out := make([]byte, 8)
	copy(out, "PB01")
	binary.BigEndian.PutUint32(out[4:], version)
	return append(out, raw...), nil
}
func CipherVersion(raw []byte) (uint32, error) {
	if len(raw) < 36 || string(raw[:4]) != "PB01" {
		return 0, errors.New("Invalid ciphertext")
	}
	v := binary.BigEndian.Uint32(raw[4:8])
	if v == 0 {
		return 0, errors.New("Invalid ciphertext")
	}
	return v, nil
}
func Decrypt(key []byte, stack string, raw []byte) ([]byte, error) {
	v, e := CipherVersion(raw)
	if e != nil {
		return nil, e
	}
	return Open(key, raw[8:], fmt.Sprintf("pb1:stack:%s:key:%d", stack, v))
}
