package returns

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"
)

func TokenFingerprint(canonical []byte) string {
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:])[:16]
}

// EncodeToken is base64url(payload) + "." + fingerprint.
func EncodeToken(canonical []byte) string {
	return base64.RawURLEncoding.EncodeToString(canonical) + "." + TokenFingerprint(canonical)
}

// DecodeToken splits and decodes a token without validating the payload; the caller re-derives the fingerprint
// from its own canonical form so a token of another kind never verifies.
func DecodeToken(token string) (payload []byte, fingerprint string, err error) {
	encoded, fingerprint, ok := strings.Cut(strings.TrimSpace(token), ".")
	if !ok || encoded == "" || fingerprint == "" {
		return nil, "", fmt.Errorf("not a token")
	}
	payload, err = base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return nil, "", fmt.Errorf("payload is not base64url")
	}
	return payload, fingerprint, nil
}
