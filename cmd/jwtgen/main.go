package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func main() {
	secret := flag.String("secret", "", "JWT signing secret (required, ≥32 bytes)")
	subject := flag.String("subject", "admin", "token subject (sub claim)")
	issuer := flag.String("issuer", "minestrate", "token issuer (iss claim)")
	ttl := flag.Int("ttl", 3600, "token lifetime in seconds")
	scopes := flag.String("scopes", "server:create", "comma-separated scope list")
	audience := flag.String("audience", "", "token audience (aud claim, optional)")
	flag.Parse()

	if *secret == "" {
		fmt.Fprintln(os.Stderr, "error: --secret is required")
		flag.Usage()
		os.Exit(1)
	}

	if len(*secret) < 32 {
		fmt.Fprintf(os.Stderr, "error: secret must be ≥32 bytes (got %d)\n", len(*secret))
		os.Exit(1)
	}

	now := time.Now()
	claims := &struct {
		Scope []string `json:"scope"`
		jwt.RegisteredClaims
	}{
		Scope: strings.Split(*scopes, ","),
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(now.Add(time.Duration(*ttl) * time.Second)),
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			Issuer:    *issuer,
			Subject:   *subject,
		},
	}

	if *audience != "" {
		claims.Audience = jwt.ClaimStrings{*audience}
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	ss, err := token.SignedString([]byte(*secret))
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: failed to sign token: %v\n", err)
		os.Exit(1)
	}

	fmt.Println(ss)
}
