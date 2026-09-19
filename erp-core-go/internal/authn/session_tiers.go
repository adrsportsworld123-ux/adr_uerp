package authn

import "time"

// Session timeout tiers per pos_frd_complete.md ("Timeout (Recommended)"):
// POS 15 min, Branch Manager 30 min, Admin 60 min. Previously every login
// got a flat 24h token regardless of role — found during the Phase 1 gap
// analysis, since nothing in the FRD's 15/30/60 tiering was ever wired up.
//
// Role names are matched case-insensitively against roles.name as seeded
// (see migrations/002_seed.sql) — there's no separate "role tier" column in
// the schema, so this is a name-based mapping rather than a stored
// property. If a user carries multiple roles, they get the longest (most
// privileged) matching tier: a Branch Manager who is also a POS User should
// not be capped at the shorter POS timeout.
var roleTiers = map[string]time.Duration{
	"pos user":       15 * time.Minute,
	"branch manager": 30 * time.Minute,
	"merchant admin": 60 * time.Minute,
}

// defaultSessionTTL is used when none of a user's roles match a known tier
// — deliberately the shortest tier, not TokenIssuer's configured fallback,
// since an unrecognized role should never imply a longer-lived session than
// the FRD's baseline.
const defaultSessionTTL = 15 * time.Minute

func sessionTTLForRoles(roles []string) time.Duration {
	best := time.Duration(0)
	for _, r := range roles {
		if ttl, ok := roleTiers[normalizeRoleName(r)]; ok && ttl > best {
			best = ttl
		}
	}
	if best == 0 {
		return defaultSessionTTL
	}
	return best
}

func normalizeRoleName(r string) string {
	out := make([]byte, len(r))
	for i := 0; i < len(r); i++ {
		c := r[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		out[i] = c
	}
	return string(out)
}
