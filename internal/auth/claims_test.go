package auth_test

import (
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/mitsuakki/minestrate/internal/auth"
)

func TestClaims(t *testing.T) {
	now := time.Now()

	claims := auth.Claims{
		Scope: []string{"read", "write"},
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    "test-suite",
			Subject:   "user-123",
			ExpiresAt: jwt.NewNumericDate(now.Add(time.Hour)),
			IssuedAt:  jwt.NewNumericDate(now),
		},
	}

	if len(claims.Scope) != 2 {
		t.Fatalf("expected 2 scopes, got %d", len(claims.Scope))
	}

	if claims.Scope[0] != "read" {
		t.Fatalf("expected first scope to be %q, got %q", "read", claims.Scope[0])
	}

	if claims.Issuer != "test-suite" {
		t.Fatalf("expected issuer %q, got %q", "test-suite", claims.Issuer)
	}

	if claims.Subject != "user-123" {
		t.Fatalf("expected subject %q, got %q", "user-123", claims.Subject)
	}

	if claims.ExpiresAt == nil {
		t.Fatal("expected ExpiresAt to be set")
	}

	if claims.IssuedAt == nil {
		t.Fatal("expected IssuedAt to be set")
	}
}
