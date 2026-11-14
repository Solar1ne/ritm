// Package accounts owns persistent identities, credentials and revocable sessions.
// It has no dependency on HTTP, the diary domain or a particular storage adapter.
package accounts

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
	"unicode/utf8"
)

const PasswordIterations = 600000

type PasswordHash struct {
	Salt       string `json:"salt"`
	Digest     string `json:"digest"`
	Iterations int    `json:"iterations"`
}

func NormalizeLogin(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if len(value) < 3 || len(value) > 32 {
		return "", ErrLogin
	}
	for _, c := range value {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '.' || c == '_' || c == '-') {
			return "", ErrLogin
		}
	}
	return value, nil
}

func ValidatePassword(password string) error {
	n := utf8.RuneCountInString(password)
	if !utf8.ValidString(password) || n < 15 || n > 128 || len(password) > 512 || len(strings.TrimSpace(password)) == 0 {
		return ErrPassword
	}
	runes := []rune(password)
	different := false
	for _, r := range runes[1:] {
		if r != runes[0] {
			different = true
			break
		}
	}
	if !different {
		return ErrPassword
	}
	return nil
}

func hashPassword(password string) (PasswordHash, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return PasswordHash{}, err
	}
	key, err := pbkdf2.Key(sha256.New, password, salt, PasswordIterations, 32)
	if err != nil {
		return PasswordHash{}, err
	}
	return PasswordHash{Salt: base64.RawStdEncoding.EncodeToString(salt), Digest: base64.RawStdEncoding.EncodeToString(key), Iterations: PasswordIterations}, nil
}

func (h PasswordHash) valid() bool {
	salt, e1 := base64.RawStdEncoding.DecodeString(h.Salt)
	key, e2 := base64.RawStdEncoding.DecodeString(h.Digest)
	return e1 == nil && e2 == nil && len(salt) == 16 && len(key) == 32 && h.Iterations == PasswordIterations
}

func verifyPassword(h PasswordHash, password string) bool {
	if !h.valid() || len(password) > 512 {
		return false
	}
	salt, _ := base64.RawStdEncoding.DecodeString(h.Salt) // validated above
	want, _ := base64.RawStdEncoding.DecodeString(h.Digest)
	key, err := pbkdf2.Key(sha256.New, password, salt, h.Iterations, 32)
	return err == nil && subtle.ConstantTimeCompare(key, want) == 1
}

// Unknown logins perform the same KDF as existing accounts, without a real credential.
func dummyPassword() PasswordHash {
	return PasswordHash{Salt: base64.RawStdEncoding.EncodeToString(make([]byte, 16)), Digest: base64.RawStdEncoding.EncodeToString(make([]byte, 32)), Iterations: PasswordIterations}
}

func token() (string, error) {
	value := make([]byte, 32)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

func TokenHash(value string) string {
	h := sha256.Sum256([]byte(value))
	return hex.EncodeToString(h[:])
}

func newRecovery() (string, string, error) {
	value := make([]byte, 20)
	if _, err := rand.Read(value); err != nil {
		return "", "", err
	}
	code := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(value)
	parts := make([]string, 0, 8)
	for i := 0; i < len(code); i += 4 {
		parts = append(parts, code[i:i+4])
	}
	return strings.Join(parts, "-"), TokenHash(code), nil
}

func recoveryDigest(value string) (string, error) {
	value = strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(value), "-", ""))
	decoded, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(value)
	if err != nil || len(decoded) != 20 || len(value) != 32 {
		return "", errors.New("invalid recovery code")
	}
	return TokenHash(value), nil
}

func ValidPrincipal(value string) bool {
	if len(value) != 66 || !strings.Contains("gtu", value[:1]) || value[1] != '_' {
		return false
	}
	decoded, err := hex.DecodeString(value[2:])
	return err == nil && len(decoded) == 32 && strings.ToLower(value) == value
}
