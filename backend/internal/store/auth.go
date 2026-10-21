package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

const sessionDuration = 30 * 24 * time.Hour

func (s *Store) EnsureAdmin(ctx context.Context, email, password string) error {
	if err := s.EnsureDefaultOrg(ctx); err != nil {
		return err
	}
	// Sign-in looks the address up in lower case, so it is stored that way.
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" || password == "" {
		return nil
	}
	var count int
	if err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	_, err = s.Pool.Exec(ctx, `INSERT INTO users (email, password_hash, role, org_id) VALUES ($1,$2,$3,1)`, email, string(hash), RoleAdmin)
	return err
}

func (s *Store) UserExists(ctx context.Context) (bool, error) {
	var exists bool
	err := s.Pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM users)`).Scan(&exists)
	return exists, err
}

func (s *Store) CreateSession(ctx context.Context, userID int64) (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	token := hex.EncodeToString(raw)
	sum := sha256.Sum256([]byte(token))
	_, err := s.Pool.Exec(ctx, `INSERT INTO sessions (token_hash, user_id, expires_at) VALUES ($1,$2,$3)`,
		hex.EncodeToString(sum[:]), userID, time.Now().Add(sessionDuration))
	if err != nil {
		return "", err
	}
	return token, nil
}

func (s *Store) UserIDBySession(ctx context.Context, token string) (int64, bool, error) {
	sum := sha256.Sum256([]byte(token))
	var userID int64
	err := s.Pool.QueryRow(ctx, `SELECT s.user_id FROM sessions s JOIN users u ON u.id = s.user_id
		WHERE s.token_hash=$1 AND s.expires_at > now() AND NOT u.disabled`, hex.EncodeToString(sum[:])).Scan(&userID)
	if err != nil {
		if errors.Is(err, pgxErrNoRows) {
			return 0, false, nil
		}
		return 0, false, err
	}
	return userID, true, nil
}

func (s *Store) DeleteSession(ctx context.Context, token string) error {
	sum := sha256.Sum256([]byte(token))
	_, err := s.Pool.Exec(ctx, `DELETE FROM sessions WHERE token_hash=$1`, hex.EncodeToString(sum[:]))
	return err
}

func (s *Store) DeleteSessionsForUser(ctx context.Context, userID int64) error {
	_, err := s.Pool.Exec(ctx, `DELETE FROM sessions WHERE user_id=$1`, userID)
	return err
}

func (s *Store) UpdatePassword(ctx context.Context, userID int64, current, next string) error {
	var hash string
	err := s.Pool.QueryRow(ctx, `SELECT password_hash FROM users WHERE id=$1`, userID).Scan(&hash)
	if err != nil {
		if errors.Is(err, pgxErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(current)); err != nil {
		return bcrypt.ErrMismatchedHashAndPassword
	}
	newHash, err := bcrypt.GenerateFromPassword([]byte(next), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	_, err = s.Pool.Exec(ctx, `UPDATE users SET password_hash=$1 WHERE id=$2`, string(newHash), userID)
	if err != nil {
		return err
	}
	return s.DeleteSessionsForUser(ctx, userID)
}

func (s *Store) PurgeExpiredSessions(ctx context.Context) error {
	_, err := s.Pool.Exec(ctx, `DELETE FROM sessions WHERE expires_at < now()`)
	return err
}

// ResetPassword sets a new password for the account with this email and
// signs it out everywhere. It is for the operator at the server's command
// line: there is no reset by email, so without it a forgotten administrator
// password meant editing the database by hand.
func (s *Store) ResetPassword(ctx context.Context, email, next string) error {
	if len(next) < 8 {
		return errors.New("password must be at least 8 characters")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(next), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	var id int64
	err = s.Pool.QueryRow(ctx, `UPDATE users SET password_hash=$1 WHERE email=$2 RETURNING id`,
		string(hash), strings.ToLower(strings.TrimSpace(email))).Scan(&id)
	if errors.Is(err, pgxErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	return s.DeleteSessionsForUser(ctx, id)
}
