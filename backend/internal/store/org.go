package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode"
)

type orgCtxKey struct{}

func WithOrg(ctx context.Context, orgID int64) context.Context {
	return context.WithValue(ctx, orgCtxKey{}, orgID)
}

func OrgID(ctx context.Context) int64 {
	id, _ := ctx.Value(orgCtxKey{}).(int64)
	return id
}

// SetHosted tells the store it serves the multi-org hosted build.
// Only a binary built with -tags hosted ever turns it on.
func (s *Store) SetHosted(on bool) { s.hosted = on }

const (
	PlanCommunity = "community"
	PlanPro       = "pro"
)

type Organization struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Slug      string `json:"slug"`
	Plan      string `json:"plan"`
	CreatedAt string `json:"created_at"`
}

func ValidPlan(plan string) bool {
	switch strings.ToLower(strings.TrimSpace(plan)) {
	case PlanCommunity, PlanPro:
		return true
	default:
		return false
	}
}

func (s *Store) EnsureDefaultOrg(ctx context.Context) error {
	if _, err := s.Pool.Exec(ctx, `INSERT INTO organizations (id, name, slug, plan)
		VALUES (1, 'Default', 'default', 'community')
		ON CONFLICT (id) DO NOTHING`); err != nil {
		return err
	}
	_, err := s.Pool.Exec(ctx, `SELECT setval('organizations_id_seq', GREATEST((SELECT MAX(id) FROM organizations), 1))`)
	return err
}

func (s *Store) GetOrg(ctx context.Context, id int64) (*Organization, error) {
	var o Organization
	err := s.Pool.QueryRow(ctx, `SELECT id, name, slug, plan, created_at::text FROM organizations WHERE id=$1`, id).
		Scan(&o.ID, &o.Name, &o.Slug, &o.Plan, &o.CreatedAt)
	if err != nil {
		if errors.Is(err, pgxErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &o, nil
}

func (s *Store) CreateOrg(ctx context.Context, name, slug, plan string) (*Organization, error) {
	name = strings.TrimSpace(name)
	slug = strings.ToLower(strings.TrimSpace(slug))
	plan = strings.ToLower(strings.TrimSpace(plan))
	if name == "" {
		return nil, errors.New("organization name is required")
	}
	if slug == "" {
		return nil, errors.New("organization slug is required")
	}
	if !ValidPlan(plan) {
		plan = PlanCommunity
	}
	var o Organization
	err := s.Pool.QueryRow(ctx, `INSERT INTO organizations (name, slug, plan)
		VALUES ($1,$2,$3) RETURNING id, name, slug, plan, created_at::text`,
		name, slug, plan).Scan(&o.ID, &o.Name, &o.Slug, &o.Plan, &o.CreatedAt)
	if err != nil {
		if strings.Contains(err.Error(), "unique") {
			return nil, errors.New("organization slug already in use")
		}
		return nil, err
	}
	return &o, nil
}

func (s *Store) UpdateOrg(ctx context.Context, id int64, name, slug, plan string) (*Organization, error) {
	name = strings.TrimSpace(name)
	slug = strings.ToLower(strings.TrimSpace(slug))
	plan = strings.ToLower(strings.TrimSpace(plan))
	if name == "" {
		return nil, errors.New("organization name is required")
	}
	if slug == "" {
		return nil, errors.New("organization slug is required")
	}
	if !ValidPlan(plan) {
		return nil, errors.New("invalid plan")
	}
	tag, err := s.Pool.Exec(ctx, `UPDATE organizations SET name=$2, slug=$3, plan=$4 WHERE id=$1`, id, name, slug, plan)
	if err != nil {
		if strings.Contains(err.Error(), "unique") {
			return nil, errors.New("organization slug already in use")
		}
		return nil, err
	}
	if tag.RowsAffected() == 0 {
		return nil, ErrNotFound
	}
	return s.GetOrg(ctx, id)
}

func (s *Store) GetRegistration(ctx context.Context) (bool, error) {
	var v bool
	err := s.getSetting(ctx, "registration", &v)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return false, nil
		}
		return false, err
	}
	return v, nil
}

func (s *Store) SetRegistration(ctx context.Context, on bool) error {
	return s.setSetting(ctx, "registration", on)
}

func (s *Store) SeedRegistration(ctx context.Context, on bool) error {
	var n int
	if err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM settings WHERE key='registration'`).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	return s.SetRegistration(ctx, on)
}

func writeOrg(ctx context.Context) int64 {
	if id := OrgID(ctx); id > 0 {
		return id
	}
	return 1
}

func slugify(name string) string {
	var b strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	s := strings.Trim(b.String(), "-")
	if s == "" {
		s = "org"
	}
	if len(s) > 48 {
		s = strings.Trim(s[:48], "-")
	}
	if s == "" {
		s = "org"
	}
	return s
}

func (s *Store) UniqueOrgSlug(ctx context.Context, name string) (string, error) {
	base := slugify(name)
	for i := 0; i < 24; i++ {
		candidate := base
		if i > 0 {
			candidate = fmt.Sprintf("%s-%d", base, i+1)
		}
		var exists bool
		if err := s.Pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM organizations WHERE slug=$1)`, candidate).Scan(&exists); err != nil {
			return "", err
		}
		if !exists {
			return candidate, nil
		}
	}
	return "", errors.New("could not allocate organization slug")
}

// Slugify makes a URL-safe slug from a name.
func Slugify(name string) string { return slugify(name) }
