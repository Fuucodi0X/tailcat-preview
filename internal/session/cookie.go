package session

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const CookieName = "devpreview_gateway_session_71b2"

var (
	ErrMalformed = errors.New("malformed preview session")
	ErrExpired   = errors.New("preview session expired")
	ErrSignature = errors.New("invalid preview session signature")
)

type Signer struct {
	key []byte
}

func NewSigner(secret string) Signer {
	sum := sha256.Sum256([]byte("devpreview-cookie-v1\x00" + secret))
	return Signer{key: sum[:]}
}

func (s Signer) Sign(previewID string, expiresAt time.Time) string {
	payload := previewID + "." + strconv.FormatInt(expiresAt.Unix(), 10)
	mac := hmac.New(sha256.New, s.key)
	_, _ = mac.Write([]byte(payload))
	signature := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return base64.RawURLEncoding.EncodeToString([]byte(payload)) + "." + signature
}

func (s Signer) Verify(value string, now time.Time) (string, error) {
	encodedPayload, encodedSignature, ok := strings.Cut(value, ".")
	if !ok {
		return "", ErrMalformed
	}
	payload, err := base64.RawURLEncoding.DecodeString(encodedPayload)
	if err != nil {
		return "", ErrMalformed
	}
	signature, err := base64.RawURLEncoding.DecodeString(encodedSignature)
	if err != nil {
		return "", ErrMalformed
	}

	mac := hmac.New(sha256.New, s.key)
	_, _ = mac.Write(payload)
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return "", ErrSignature
	}

	previewID, expiresText, ok := strings.Cut(string(payload), ".")
	if !ok || previewID == "" {
		return "", ErrMalformed
	}
	expiresUnix, err := strconv.ParseInt(expiresText, 10, 64)
	if err != nil {
		return "", fmt.Errorf("%w: expiry", ErrMalformed)
	}
	if !now.Before(time.Unix(expiresUnix, 0)) {
		return "", ErrExpired
	}
	return previewID, nil
}
