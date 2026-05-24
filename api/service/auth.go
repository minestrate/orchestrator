package service

import (
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

type RefreshManager struct {
	secret string
	tokens sync.Map // map[string]string (tokenID -> subject)
}

func NewRefreshManager(secret string) *RefreshManager {
	return &RefreshManager{
		secret: secret,
	}
}

func (rm *RefreshManager) GenerateToken(subject string) (string, error) {
	tokenID := uuid.New().String()
	rm.tokens.Store(tokenID, subject)

	claims := &RefreshClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			ID:        tokenID,
			Subject:   subject,
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour * 24 * 7)), // 7 days
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(rm.secret))
}

func (rm *RefreshManager) ValidateToken(tokenString string) (string, error) {
	claims := &RefreshClaims{}
	token, err := jwt.ParseWithClaims(tokenString, claims, func(token *jwt.Token) (any, error) {
		return []byte(rm.secret), nil
	})

	if err != nil || !token.Valid {
		return "", err
	}

	// Check if token exists in store
	if _, ok := rm.tokens.Load(claims.ID); !ok {
		return "", jwt.ErrTokenInvalidClaims
	}

	return claims.Subject, nil
}

func (rm *RefreshManager) RevokeToken(tokenID string) {
	rm.tokens.Delete(tokenID)
}

func (rm *RefreshManager) GetSecret() string {
	return rm.secret
}
