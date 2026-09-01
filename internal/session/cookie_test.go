package session

import (
	"errors"
	"testing"
	"time"
)

func TestSignerRoundTrip(t *testing.T) {
	now := time.Unix(2_000_000_000, 0)
	signer := NewSigner("control-secret")
	value := signer.Sign("preview-123", now.Add(time.Hour))

	got, err := signer.Verify(value, now)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if got != "preview-123" {
		t.Fatalf("preview ID = %q, want preview-123", got)
	}
}

func TestSignerRejectsTampering(t *testing.T) {
	now := time.Unix(2_000_000_000, 0)
	signer := NewSigner("control-secret")
	value := signer.Sign("preview-123", now.Add(time.Hour))
	value = "x" + value[1:]

	if _, err := signer.Verify(value, now); err == nil {
		t.Fatal("Verify accepted a tampered cookie")
	}
}

func TestSignerRejectsExpiredCookie(t *testing.T) {
	now := time.Unix(2_000_000_000, 0)
	signer := NewSigner("control-secret")
	value := signer.Sign("preview-123", now.Add(-time.Second))

	_, err := signer.Verify(value, now)
	if !errors.Is(err, ErrExpired) {
		t.Fatalf("Verify error = %v, want ErrExpired", err)
	}
}
