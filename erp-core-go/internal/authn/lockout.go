package authn

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// recordFailedLoginAttempt and clearFailedLoginState are shared between
// password login (handlers.go) and PIN login (pin_handlers.go) — both are
// ways of authenticating the same users row, so lockout state (and the
// 5-attempt/15-minute policy) is shared between them rather than each
// login method keeping its own counter.

func recordFailedLoginAttempt(ctx context.Context, tx pgx.Tx, userID string, currentAttempts int) error {
	attempts := currentAttempts + 1
	if attempts >= maxFailedAttempts {
		_, err := tx.Exec(ctx, `
			UPDATE users SET failed_login_attempts = 0, locked_until = now() + $1::interval
			WHERE id = $2`, lockoutDuration.String(), userID)
		return err
	}
	_, err := tx.Exec(ctx, `UPDATE users SET failed_login_attempts = $1 WHERE id = $2`, attempts, userID)
	return err
}

func clearFailedLoginState(ctx context.Context, tx pgx.Tx, userID string, hadFailures bool) error {
	if !hadFailures {
		return nil
	}
	_, err := tx.Exec(ctx, `UPDATE users SET failed_login_attempts = 0, locked_until = NULL WHERE id = $1`, userID)
	return err
}

func fetchRoles(ctx context.Context, tx pgx.Tx, userID string) ([]string, error) {
	rows, err := tx.Query(ctx, `
		SELECT r.name FROM roles r
		JOIN user_roles ur ON ur.role_id = r.id
		WHERE ur.user_id = $1`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var roles []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		roles = append(roles, name)
	}
	return roles, rows.Err()
}
