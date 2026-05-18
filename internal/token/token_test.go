package token

import (
	"testing"
	"time"
)

func TestSignVerifyRoundTrip(t *testing.T) {
	s := New("secret-a")
	tok, err := s.Sign(Claims{Kind: KindEvent, EventID: "e1",
		ExpiresAt: time.Now().Add(time.Hour).Unix()})
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.Verify(tok, KindEvent)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if c.EventID != "e1" {
		t.Fatalf("event id = %q", c.EventID)
	}
}

func TestExpired(t *testing.T) {
	s := New("k")
	tok, _ := s.Sign(Claims{Kind: KindSession, ExpiresAt: time.Now().Add(-time.Second).Unix()})
	if _, err := s.Verify(tok, KindSession); err != ErrExpired {
		t.Fatalf("want ErrExpired, got %v", err)
	}
}

func TestKindMismatch(t *testing.T) {
	s := New("k")
	tok, _ := s.Sign(Claims{Kind: KindAuth, ExpiresAt: time.Now().Add(time.Hour).Unix()})
	if _, err := s.Verify(tok, KindEvent); err != ErrKind {
		t.Fatalf("want ErrKind, got %v", err)
	}
}

func TestRoleParticipantRoundTrip(t *testing.T) {
	s := New("secret-b")
	tok, err := s.Sign(Claims{Kind: KindAuth, Role: RoleParticipant, Subject: "p42",
		ExpiresAt: time.Now().Add(time.Hour).Unix()})
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.Verify(tok, KindAuth)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if c.Role != RoleParticipant {
		t.Fatalf("role = %q, want %q", c.Role, RoleParticipant)
	}
	if c.Subject != "p42" {
		t.Fatalf("subject = %q", c.Subject)
	}
}

func TestTamperAndWrongSecret(t *testing.T) {
	s := New("right")
	tok, _ := s.Sign(Claims{Kind: KindEvent, ExpiresAt: time.Now().Add(time.Hour).Unix()})
	if _, err := New("wrong").Verify(tok, KindEvent); err != ErrSignature {
		t.Fatalf("want ErrSignature for wrong secret, got %v", err)
	}
	if _, err := s.Verify(tok+"x", KindEvent); err == nil {
		t.Fatal("tampered token verified")
	}
}
