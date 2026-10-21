package store

import (
	"context"
	"errors"
	"strings"
	"sync"

	"golang.org/x/crypto/bcrypt"
)

const (
	RoleAdmin    = "admin"
	RoleOperator = "operator"
	RoleViewer   = "viewer"
)

type User struct {
	ID          int64  `json:"id"`
	Email       string `json:"email"`
	Name        string `json:"name"`
	Role        string `json:"role"`
	Disabled    bool   `json:"disabled"`
	CreatedAt   string `json:"created_at"`
	HasPassword bool   `json:"has_password"`
	OIDC        bool   `json:"oidc,omitempty"`
	OrgID       int64  `json:"org_id"`
}

func ValidRole(role string) bool {
	switch role {
	case RoleAdmin, RoleOperator, RoleViewer:
		return true
	default:
		return false
	}
}

func (s *Store) GetUser(ctx context.Context, id int64) (*User, error) {
	var u User
	err := s.Pool.QueryRow(ctx, `SELECT id, email, name, role, disabled, created_at::text,
		password_hash <> '', oidc_sub IS NOT NULL AND oidc_sub <> '', org_id
		FROM users WHERE id=$1 AND ($2 = 0 OR org_id=$2)`, id, OrgID(ctx)).Scan(&u.ID, &u.Email, &u.Name, &u.Role, &u.Disabled, &u.CreatedAt, &u.HasPassword, &u.OIDC, &u.OrgID)
	if err != nil {
		if errors.Is(err, pgxErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &u, nil
}

func (s *Store) ListUsers(ctx context.Context) ([]User, error) {
	rows, err := s.Pool.Query(ctx, `SELECT id, email, name, role, disabled, created_at::text,
		password_hash <> '', oidc_sub IS NOT NULL AND oidc_sub <> '', org_id
		FROM users WHERE ($1 = 0 OR org_id=$1) ORDER BY email ASC, id ASC`, OrgID(ctx))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []User
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.ID, &u.Email, &u.Name, &u.Role, &u.Disabled, &u.CreatedAt, &u.HasPassword, &u.OIDC, &u.OrgID); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	if out == nil {
		out = []User{}
	}
	return out, rows.Err()
}

func (s *Store) CountUsers(ctx context.Context) (int, error) {
	var n int
	err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM users WHERE ($1 = 0 OR org_id=$1)`, OrgID(ctx)).Scan(&n)
	return n, err
}

func (s *Store) CountAdmins(ctx context.Context) (int, error) {
	var n int
	err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM users WHERE role=$1 AND NOT disabled AND ($2 = 0 OR org_id=$2)`, RoleAdmin, OrgID(ctx)).Scan(&n)
	return n, err
}

func (s *Store) CreateUser(ctx context.Context, email, name, role, password string) (*User, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	name = strings.TrimSpace(name)
	role = strings.ToLower(strings.TrimSpace(role))
	if email == "" {
		return nil, errors.New("email is required")
	}
	if !ValidRole(role) {
		return nil, errors.New("invalid role")
	}
	if len(password) < 8 {
		return nil, errors.New("password must be at least 8 characters")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}
	var u User
	org := writeOrg(ctx)
	err = s.Pool.QueryRow(ctx, `INSERT INTO users (email, name, role, password_hash, org_id)
		VALUES ($1,$2,$3,$4,$5) RETURNING id, email, name, role, disabled, created_at::text, password_hash <> '', FALSE, org_id`,
		email, name, role, string(hash), org).Scan(&u.ID, &u.Email, &u.Name, &u.Role, &u.Disabled, &u.CreatedAt, &u.HasPassword, &u.OIDC, &u.OrgID)
	if err != nil {
		if strings.Contains(err.Error(), "unique") {
			return nil, errors.New("email already in use")
		}
		return nil, err
	}
	return &u, nil
}

func (s *Store) UpdateUser(ctx context.Context, id int64, name, role string, disabled bool) (*User, error) {
	name = strings.TrimSpace(name)
	role = strings.ToLower(strings.TrimSpace(role))
	if !ValidRole(role) {
		return nil, errors.New("invalid role")
	}
	if role != RoleAdmin || disabled {
		n, err := s.CountAdmins(ctx)
		if err != nil {
			return nil, err
		}
		cur, err := s.GetUser(ctx, id)
		if err != nil {
			return nil, err
		}
		if cur.Role == RoleAdmin && !cur.Disabled && n <= 1 {
			return nil, errors.New("cannot remove the last admin")
		}
	}
	tag, err := s.Pool.Exec(ctx, `UPDATE users SET name=$2, role=$3, disabled=$4 WHERE id=$1 AND ($5 = 0 OR org_id=$5)`, id, name, role, disabled, OrgID(ctx))
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() == 0 {
		return nil, ErrNotFound
	}
	if disabled {
		_ = s.DeleteSessionsForUser(ctx, id)
	}
	return s.GetUser(ctx, id)
}

func (s *Store) DeleteUser(ctx context.Context, id int64) error {
	cur, err := s.GetUser(ctx, id)
	if err != nil {
		return err
	}
	if cur.Role == RoleAdmin && !cur.Disabled {
		n, err := s.CountAdmins(ctx)
		if err != nil {
			return err
		}
		if n <= 1 {
			return errors.New("cannot delete the last admin")
		}
	}
	tag, err := s.Pool.Exec(ctx, `DELETE FROM users WHERE id=$1 AND ($2 = 0 OR org_id=$2)`, id, OrgID(ctx))
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

var (
	timingHashOnce sync.Once
	timingHashVal  []byte
)

// timingHash is a hash of nothing anyone knows, at the cost real passwords use.
func timingHash() []byte {
	timingHashOnce.Do(func() {
		timingHashVal, _ = bcrypt.GenerateFromPassword([]byte("argus-timing-equaliser"), bcrypt.DefaultCost)
	})
	return timingHashVal
}

func (s *Store) VerifyLogin(ctx context.Context, email, password string) (int64, error) {
	var id int64
	var hash string
	var disabled bool
	err := s.Pool.QueryRow(ctx, `SELECT id, password_hash, disabled FROM users WHERE email=$1`,
		strings.ToLower(strings.TrimSpace(email))).Scan(&id, &hash, &disabled)
	if err != nil {
		if errors.Is(err, pgxErrNoRows) {
			// Spend the same time as a real account, so response times do
			// not reveal which emails have one.
			_ = bcrypt.CompareHashAndPassword(timingHash(), []byte(password))
			return 0, bcrypt.ErrMismatchedHashAndPassword
		}
		return 0, err
	}
	if disabled || hash == "" {
		_ = bcrypt.CompareHashAndPassword(timingHash(), []byte(password))
		return 0, bcrypt.ErrMismatchedHashAndPassword
	}
	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)); err != nil {
		return 0, err
	}
	return id, nil
}

// ErrEmailUnverified is a single sign-on identity claiming the email of an
// account it is not yet linked to, without the provider vouching for it.
var ErrEmailUnverified = errors.New("email is not verified by the identity provider")

// FindOrCreateOIDCUser returns the account for a single sign-on identity:
// the one already linked to sub, else the one with this email, else a new
// one. Linking by email hands over an existing account, the first
// administrator's included, so it needs the provider's word that the address
// belongs to whoever signed in. Where users can set their own email at the
// provider, anyone could otherwise claim an administrator's.
func (s *Store) FindOrCreateOIDCUser(ctx context.Context, sub, email, name, role string, emailVerified bool) (*User, error) {
	sub = strings.TrimSpace(sub)
	email = strings.ToLower(strings.TrimSpace(email))
	name = strings.TrimSpace(name)
	if sub == "" || email == "" {
		return nil, errors.New("oidc subject and email are required")
	}
	if !ValidRole(role) {
		role = RoleViewer
	}
	var id int64
	err := s.Pool.QueryRow(ctx, `SELECT id FROM users WHERE oidc_sub=$1`, sub).Scan(&id)
	if err == nil {
		return s.GetUser(ctx, id)
	}
	if !errors.Is(err, pgxErrNoRows) {
		return nil, err
	}
	err = s.Pool.QueryRow(ctx, `SELECT id FROM users WHERE email=$1`, email).Scan(&id)
	if err == nil {
		if !emailVerified {
			return nil, ErrEmailUnverified
		}
		_, err = s.Pool.Exec(ctx, `UPDATE users SET oidc_sub=$2, name=CASE WHEN name='' THEN $3 ELSE name END WHERE id=$1`, id, sub, name)
		if err != nil {
			return nil, err
		}
		return s.GetUser(ctx, id)
	}
	if !errors.Is(err, pgxErrNoRows) {
		return nil, err
	}
	var u User
	err = s.Pool.QueryRow(ctx, `INSERT INTO users (email, name, role, password_hash, oidc_sub, org_id)
		VALUES ($1,$2,$3,'',$4,$5)
		RETURNING id, email, name, role, disabled, created_at::text, password_hash <> '', TRUE, org_id`,
		email, name, role, sub, writeOrg(ctx)).Scan(&u.ID, &u.Email, &u.Name, &u.Role, &u.Disabled, &u.CreatedAt, &u.HasPassword, &u.OIDC, &u.OrgID)
	if err != nil {
		if strings.Contains(err.Error(), "unique") {
			return nil, errors.New("email already in use")
		}
		return nil, err
	}
	return &u, nil
}
