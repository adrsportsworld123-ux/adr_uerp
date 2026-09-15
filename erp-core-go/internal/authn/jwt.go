package authn

import (
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// TokenIssuer signs and verifies access tokens with a single HS256 secret.
// This is intentionally the ONLY piece of auth logic that knows about the
// signing algorithm/secret — everything else in the codebase deals only in
// *Claims, so swapping this for RS256 + key rotation, or for validating
// tokens issued by Keycloak/an external IdP later, touches this file alone.
type TokenIssuer struct {
	secret    []byte
	issuer    string
	accessTTL time.Duration
}

func NewTokenIssuer(secret, issuer string, accessTTL time.Duration) *TokenIssuer {
	return &TokenIssuer{secret: []byte(secret), issuer: issuer, accessTTL: accessTTL}
}

func (t *TokenIssuer) IssueAccessToken(tenantID, userID, branchID string, roles []string) (string, time.Time, error) {
	expiresAt := time.Now().Add(t.accessTTL)
	claims := &Claims{
		TenantID: tenantID,
		UserID:   userID,
		BranchID: branchID,
		Roles:    roles,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    t.issuer,
			Subject:   userID,
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			ExpiresAt: jwt.NewNumericDate(expiresAt),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString(t.secret)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("authn: sign token: %w", err)
	}
	return signed, expiresAt, nil
}

func (t *TokenIssuer) Parse(rawToken string) (*Claims, error) {
	claims := &Claims{}
	token, err := jwt.ParseWithClaims(rawToken, claims, func(tok *jwt.Token) (interface{}, error) {
		if _, ok := tok.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", tok.Header["alg"])
		}
		return t.secret, nil
	})
	if err != nil {
		return nil, fmt.Errorf("authn: parse token: %w", err)
	}
	if !token.Valid {
		return nil, fmt.Errorf("authn: token invalid")
	}
	return claims, nil
}
