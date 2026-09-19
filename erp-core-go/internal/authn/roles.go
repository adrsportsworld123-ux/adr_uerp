package authn

// HasRole reports whether roles contains want, case-insensitively — the
// one shared place other packages should do a role-name check (discount
// authorization, inventory-adjustment permission) rather than each
// re-implementing its own case-folding comparison.
func HasRole(roles []string, want string) bool {
	target := normalizeRoleName(want)
	for _, r := range roles {
		if normalizeRoleName(r) == target {
			return true
		}
	}
	return false
}
