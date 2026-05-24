package service

import (
	"testing"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
)

func TestRefreshManager(t *testing.T) {
	secret := "test-secret"
	rm := NewRefreshManager(secret)

	// Test GenerateToken
	subject := "test-user"
	tokenString, err := rm.GenerateToken(subject)
	assert.NoError(t, err)
	assert.NotEmpty(t, tokenString)

	// Test ValidateToken
	sub, err := rm.ValidateToken(tokenString)
	assert.NoError(t, err)
	assert.Equal(t, subject, sub)

	// Test ValidateToken with invalid token
	_, err = rm.ValidateToken("invalid-token")
	assert.Error(t, err)

	// Test RevokeToken
	claims := &RefreshClaims{}
	_, _ = jwt.ParseWithClaims(tokenString, claims, func(token *jwt.Token) (any, error) {
		return []byte(secret), nil
	})
	tokenID := claims.ID

	rm.RevokeToken(tokenID)
	_, err = rm.ValidateToken(tokenString)
	assert.Error(t, err)
}

func TestGetSecret(t *testing.T) {
	secret := "test-secret"
	rm := NewRefreshManager(secret)
	assert.Equal(t, secret, rm.GetSecret())
}
