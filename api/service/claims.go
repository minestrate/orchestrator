package service

import "github.com/golang-jwt/jwt/v5"

type Claims struct {
	Scope []string `json:"scope"`
	jwt.RegisteredClaims
}

type RefreshClaims struct {
	jwt.RegisteredClaims
}