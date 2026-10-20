package vault

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

type Vault struct{ aead cipher.AEAD }

func New(key []byte) (*Vault, error) {
	b, e := aes.NewCipher(key)
	if e != nil {
		return nil, e
	}
	a, e := cipher.NewGCM(b)
	return &Vault{a}, e
}
func Open(path string) (*Vault, error) {
	b, e := os.ReadFile(path)
	if os.IsNotExist(e) {
		if e = os.MkdirAll(filepath.Dir(path), 0700); e != nil {
			return nil, e
		}
		b = make([]byte, 32)
		if _, e = rand.Read(b); e != nil {
			return nil, e
		}
		f, e := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e != nil {
			return nil, e
		}
		_, e = f.Write(b)
		if e == nil {
			e = f.Sync()
		}
		ce := f.Close()
		if e != nil {
			return nil, e
		}
		if ce != nil {
			return nil, ce
		}
	} else if e != nil {
		return nil, e
	}
	if len(b) != 32 {
		return nil, fmt.Errorf("integration encryption key must be 32 bytes")
	}
	if e = os.Chmod(path, 0600); e != nil {
		return nil, e
	}
	return New(b)
}
func (v *Vault) Seal(b []byte) (string, error) {
	n := make([]byte, v.aead.NonceSize())
	if _, e := io.ReadFull(rand.Reader, n); e != nil {
		return "", e
	}
	return base64.StdEncoding.EncodeToString(v.aead.Seal(n, n, b, []byte("maacc-integrations-v1"))), nil
}
func (v *Vault) Open(s string) ([]byte, error) {
	b, e := base64.StdEncoding.DecodeString(s)
	n := v.aead.NonceSize()
	if e != nil || len(b) < n {
		return nil, fmt.Errorf("encrypted settings invalid")
	}
	return v.aead.Open(nil, b[:n], b[n:], []byte("maacc-integrations-v1"))
}
