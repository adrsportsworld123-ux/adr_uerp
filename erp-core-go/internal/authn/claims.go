package authn

import (
	"context"

	"github.com/golang-jwt/jwt/v5"
)

// Claims is the JWT payload every other service in the system trusts.
// TenantID is the field the whole multi-tenancy model hinges on — see
// db.WithTenant, which every handler below the middleware must route
// through using this value, never a client-supplied tenant id.
type Claims struct {
	TenantID string   `json:"tenant_id"`
	UserID   string   `json:"user_id"`
	BranchID string   `json:"branch_id,omitempty"`
	Roles    []string `json:"roles"`
	jwt.RegisteredClaims
}

type ctxKey int

const claimsCtxKey ctxKey = iota

func WithClaims(ctx context.Context, c *Claims) context.Context {
	return context.WithValue(ctx, claimsCtxKey, c)
}

// FromContext retrieves the authenticated request's claims. Every handler
// on a route behind RequireAuth can call this and trust the result — it
// is only ever absent if RequireAuth was skipped by mistake, which is a
// routing bug worth panicking loudly about rather than silently treating
// as "no tenant" (see middleware.go).
func FromContext(ctx context.Context) (*Claims, bool) {
	c, ok := ctx.Value(claimsCtxKey).(*Claims)
	return c, ok
}
