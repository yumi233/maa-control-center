package vault

import (
	"os"
	"path/filepath"
	"testing"
)

func TestVaultPersistsKeyAndRejectsTampering(t *testing.T) {
	p := filepath.Join(t.TempDir(), "integrations.key")
	v, e := Open(p)
	if e != nil {
		t.Fatal(e)
	}
	cipher, e := v.Seal([]byte("fixture-secret"))
	if e != nil {
		t.Fatal(e)
	}
	again, e := Open(p)
	if e != nil {
		t.Fatal(e)
	}
	plain, e := again.Open(cipher)
	if e != nil || string(plain) != "fixture-secret" {
		t.Fatal("key not restored")
	}
	other, _ := New(make([]byte, 32))
	if _, e = other.Open(cipher); e == nil {
		t.Fatal("wrong key accepted")
	}
	if _, e = again.Open(cipher[:10]); e == nil {
		t.Fatal("tampering accepted")
	}
	st, _ := os.Stat(p)
	if st.Mode().Perm() != 0600 {
		t.Fatal("key permissions")
	}
}
