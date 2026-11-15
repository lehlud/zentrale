package domain

import (
	"crypto/rand"
	"encoding/base64"
)

// NewID returns a 17-character, URL-safe resource reference with 96 random bits.
// Existing identifiers remain opaque and valid. Secrets, tokens and integrity
// fingerprints use their own generators and must not be shortened through this.
func NewID() string {
	var value [12]byte
	if _, err := rand.Read(value[:]); err != nil {
		panic("generate resource ID: " + err.Error())
	}
	// A letter prefix also satisfies machine/VM scope identifiers.
	return "r" + base64.RawURLEncoding.EncodeToString(value[:])
}
