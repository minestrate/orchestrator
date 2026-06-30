package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func TestAuth(t *testing.T) {
	secret := "this-is-a-very-long-secret-key-32-bytes"

	tests := []struct {
		name           string
		tokenFunc      func() string
		expectedStatus int
	}{
		{
			name: "valid token",
			tokenFunc: func() string {
				claims := &Claims{
					Scope: []string{"server:create"},
					RegisteredClaims: jwt.RegisteredClaims{
						Subject:   "player-123",
						ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
					},
				}
				token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
				ss, _ := token.SignedString([]byte(secret))
				return "Bearer " + ss
			},
			expectedStatus: http.StatusOK,
		},
		{
			name: "missing header",
			tokenFunc: func() string {
				return ""
			},
			expectedStatus: http.StatusUnauthorized,
		},
		{
			name: "malformed header",
			tokenFunc: func() string {
				return "invalid-header"
			},
			expectedStatus: http.StatusUnauthorized,
		},
		{
			name: "wrong secret",
			tokenFunc: func() string {
				claims := &Claims{
					RegisteredClaims: jwt.RegisteredClaims{
						ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
					},
				}
				token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
				ss, _ := token.SignedString([]byte("wrong-secret"))
				return "Bearer " + ss
			},
			expectedStatus: http.StatusUnauthorized,
		},
		{
			name: "expired token",
			tokenFunc: func() string {
				claims := &Claims{
					RegisteredClaims: jwt.RegisteredClaims{
						ExpiresAt: jwt.NewNumericDate(time.Now().Add(-time.Hour)),
					},
				}
				token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
				ss, _ := token.SignedString([]byte(secret))
				return "Bearer " + ss
			},
			expectedStatus: http.StatusUnauthorized,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := Auth(secret)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tt.name == "valid token" {
					claims, ok := r.Context().Value(ClaimsKey).(*Claims)
					if !ok {
						t.Error("claims not found in context")
					} else if claims.Subject != "player-123" {
						t.Errorf("expected subject player-123, got %s", claims.Subject)
					}
				}
				w.WriteHeader(http.StatusOK)
			}))

			req := httptest.NewRequest(http.MethodGet, "/", nil)
			if token := tt.tokenFunc(); token != "" {
				req.Header.Set("Authorization", token)
			}
			rr := httptest.NewRecorder()

			handler.ServeHTTP(rr, req)

			if rr.Code != tt.expectedStatus {
				t.Errorf("expected status %d, got %d", tt.expectedStatus, rr.Code)
			}
		})
	}
}

func TestRequireScope(t *testing.T) {
	secret := "this-is-a-very-long-secret-key-32-bytes"

	tests := []struct {
		name           string
		scopes         []string
		requiredScope  string
		expectedStatus int
	}{
		{
			name:           "valid scope",
			scopes:         []string{"server:create", "server:read"},
			requiredScope:  "server:create",
			expectedStatus: http.StatusOK,
		},
		{
			name:           "missing scope",
			scopes:         []string{"server:read"},
			requiredScope:  "server:create",
			expectedStatus: http.StatusForbidden,
		},
		{
			name:           "no scopes",
			scopes:         []string{},
			requiredScope:  "server:create",
			expectedStatus: http.StatusForbidden,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := Auth(secret)(RequireScope(tt.requiredScope)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			})))

			claims := &Claims{
				Scope: tt.scopes,
				RegisteredClaims: jwt.RegisteredClaims{
					ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
				},
			}
			token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
			ss, _ := token.SignedString([]byte(secret))

			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.Header.Set("Authorization", "Bearer "+ss)
			rr := httptest.NewRecorder()

			handler.ServeHTTP(rr, req)

			if rr.Code != tt.expectedStatus {
				t.Errorf("expected status %d, got %d", tt.expectedStatus, rr.Code)
			}
		})
	}
}
