package secure

import (
	"bytes"
	"testing"
)

func TestAuthenticatedMessageEncryption(t *testing.T) {
	box, e := NewBox(bytes.Repeat([]byte{7}, 32))
	if e != nil {
		t.Fatal(e)
	}
	a, e := box.Encrypt("synthetic private message", "intent:1")
	if e != nil {
		t.Fatal(e)
	}
	b, _ := box.Encrypt("synthetic private message", "intent:1")
	if bytes.Equal(a, b) || bytes.Contains(a, []byte("synthetic private message")) {
		t.Fatal("nonce reuse or plaintext persistence")
	}
	p, e := box.Decrypt(a, "intent:1")
	if e != nil || p != "synthetic private message" {
		t.Fatal("roundtrip", e)
	}
	if _, e = box.Decrypt(a, "intent:2"); e == nil {
		t.Fatal("version substitution accepted")
	}
	a[len(a)-1] ^= 1
	if _, e = box.Decrypt(a, "intent:1"); e == nil {
		t.Fatal("tampering accepted")
	}
	if _, e = box.Decrypt([]byte{}, "intent:1"); e == nil {
		t.Fatal("empty envelope accepted")
	}
}
func TestPasswordHashAndOpaqueTokens(t *testing.T) {
	h, e := PasswordHash("synthetic-valid-password")
	if e != nil {
		t.Fatal(e)
	}
	if !VerifyPassword(h, "synthetic-valid-password") || VerifyPassword(h, "wrong") || VerifyPassword("$argon2id$v=19$m=999999999$bad$bad", "synthetic-valid-password") {
		t.Fatal("password verification")
	}
	a, _ := Token()
	b, _ := Token()
	if len(a) != 43 || a == b {
		t.Fatal("bad session tokens")
	}
}
