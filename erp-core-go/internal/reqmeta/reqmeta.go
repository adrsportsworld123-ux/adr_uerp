// Package reqmeta carries who a request turned out to be acting for, from
// a handler back out to the middleware that wrapped it. Only the
// unauthenticated login routes need it: until they resolve a merchant
// and a user there are no JWT claims, so the activity log
// (internal/activity) learns them here instead. It has no dependencies
// so that authn can report into it without importing the activity log.
package reqmeta

import (
	"context"
	"sync"
)

type Actor struct {
	mu       sync.Mutex
	tenantID string
	userID   string
}

type key struct{}

// WithActor attaches an empty slot to ctx.
func WithActor(ctx context.Context) (context.Context, *Actor) {
	a := &Actor{}
	return context.WithValue(ctx, key{}, a), a
}

// SetActor records the tenant and (when known) the user the request acted
// for. A no-op when nothing attached a slot.
func SetActor(ctx context.Context, tenantID, userID string) {
	a, ok := ctx.Value(key{}).(*Actor)
	if !ok {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.tenantID = tenantID
	if userID != "" {
		a.userID = userID
	}
}

// Get returns what SetActor recorded.
func (a *Actor) Get() (tenantID, userID string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.tenantID, a.userID
}
