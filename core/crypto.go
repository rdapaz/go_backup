package core

import (
	crand "crypto/rand"
	"encoding/base64"

	"golang.org/x/crypto/scrypt"
)

const (
	scryptN      = 1 << 15
	scryptR      = 8
	scryptP      = 1
	scryptKeyLen = 32
)

func GenerateStrongPassword() (string, error) {
	buf := make([]byte, 32)
	if _, err := crand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func DerivePasswordHash(password string) (hashB64 string, salt []byte, err error) {
	salt = make([]byte, 16)
	if _, err = crand.Read(salt); err != nil {
		return
	}

	derived, err := scrypt.Key([]byte(password), salt, scryptN, scryptR, scryptP, scryptKeyLen)
	if err != nil {
		return
	}

	hashB64 = base64.StdEncoding.EncodeToString(derived)
	return
}
