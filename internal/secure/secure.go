package secure

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"strings"

	"golang.org/x/crypto/argon2"
)

// Argon2id parameters follow RFC 9106's memory-constrained recommendation.
func PasswordHash(password string) (string, error) {
	if len(password) < 12 || len(password) > 256 {
		return "", errors.New("password must contain 12 to 256 bytes")
	}
	salt := make([]byte, 16)
	if _, e := rand.Read(salt); e != nil {
		return "", e
	}
	key := argon2.IDKey([]byte(password), salt, 3, 64*1024, 4, 32)
	return "$argon2id$v=19$m=65536,t=3,p=4$" + base64.RawStdEncoding.EncodeToString(salt) + "$" + base64.RawStdEncoding.EncodeToString(key), nil
}
func VerifyPassword(hash, password string) bool {
	p := strings.Split(hash, "$")
	if len(p) != 6 || p[1] != "argon2id" || p[2] != "v=19" || p[3] != "m=65536,t=3,p=4" || len(password) > 256 {
		return false
	}
	salt, e := base64.RawStdEncoding.DecodeString(p[4])
	if e != nil || len(salt) != 16 {
		return false
	}
	want, e := base64.RawStdEncoding.DecodeString(p[5])
	if e != nil || len(want) != 32 {
		return false
	}
	got := argon2.IDKey([]byte(password), salt, 3, 64*1024, 4, 32)
	return subtle.ConstantTimeCompare(got, want) == 1
}
func Token() (string, error) {
	b := make([]byte, 32)
	_, e := rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b), e
}

type Box struct{ aead cipher.AEAD }

func NewBox(key []byte) (*Box, error) {
	if len(key) != 32 {
		return nil, errors.New("invalid message key")
	}
	b, e := aes.NewCipher(key)
	if e != nil {
		return nil, e
	}
	a, e := cipher.NewGCM(b)
	return &Box{a}, e
}

// Associated data binds ciphertext to the exact intent/version. Envelope byte 1
// allows a future explicit key/version migration; keys never accompany records.
func (b *Box) Encrypt(text, aad string) ([]byte, error) {
	n := make([]byte, b.aead.NonceSize())
	if _, e := rand.Read(n); e != nil {
		return nil, e
	}
	out := append([]byte{1}, n...)
	return b.aead.Seal(out, n, []byte(text), []byte(aad)), nil
}
func (b *Box) Decrypt(raw []byte, aad string) (string, error) {
	if len(raw) < 1+b.aead.NonceSize()+b.aead.Overhead() || raw[0] != 1 {
		return "", errors.New("invalid encrypted envelope")
	}
	n := raw[1 : 1+b.aead.NonceSize()]
	p, e := b.aead.Open(nil, n, raw[1+b.aead.NonceSize():], []byte(aad))
	return string(p), e
}
