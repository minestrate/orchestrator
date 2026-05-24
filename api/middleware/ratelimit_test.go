package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/golang-jwt/jwt/v5"
	"github.com/mitsuakki/minestrate/api/service"
)

func TestRateLimiter(t *testing.T) {
	rl := NewRateLimiter(10, 5) // 10/s, 5 burst
	
	sub := "test-sub"
	claims := &service.Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject: sub,
		},
	}
	
	ctx := context.WithValue(context.Background(), ClaimsKey, claims)
	handler := rl.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	successCount := 0
	errorCount := 0

	for i := 0; i < 20; i++ {
		req := httptest.NewRequest("GET", "/", nil).WithContext(ctx)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		
		if w.Code == http.StatusOK {
			successCount++
		} else if w.Code == http.StatusTooManyRequests {
			errorCount++
		}
	}

	if successCount != 5 {
		t.Errorf("expected 5 successful requests (burst capacity), got %d", successCount)
	}
	if errorCount != 15 {
		t.Errorf("expected 15 rejected requests, got %d", errorCount)
	}
}
