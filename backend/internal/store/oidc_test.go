package store

import (
	"context"
	"errors"
	"testing"
)

// Single sign-on joins an existing account by email only on the provider's
// word that the address was verified. Without it, anyone able to set their
// own email at the provider could sign in as the first administrator.
func TestSingleSignOnLinksOnlyAVerifiedEmail(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	if err := st.EnsureAdmin(ctx, "admin@example.com", "change-me-too"); err != nil {
		t.Fatal(err)
	}
	linked := func() string {
		var sub string
		if err := st.Pool.QueryRow(ctx, `SELECT COALESCE(oidc_sub,'') FROM users WHERE email='admin@example.com'`).Scan(&sub); err != nil {
			t.Fatal(err)
		}
		return sub
	}

	if _, err := st.FindOrCreateOIDCUser(ctx, "attacker-sub", "Admin@Example.com", "Mallory", RoleViewer, false); !errors.Is(err, ErrEmailUnverified) {
		t.Fatalf("an unverified claim to the administrator's email: %v", err)
	}
	if sub := linked(); sub != "" {
		t.Fatalf("the administrator's account was linked to %q", sub)
	}

	// Someone new is given their own account either way.
	fresh, err := st.FindOrCreateOIDCUser(ctx, "new-sub", "new@example.com", "New", RoleViewer, false)
	if err != nil || fresh.Role != RoleViewer || fresh.Email != "new@example.com" {
		t.Fatalf("a first sign-in should create an account: %+v %v", fresh, err)
	}

	owner, err := st.FindOrCreateOIDCUser(ctx, "admin-sub", "admin@example.com", "Admin", RoleViewer, true)
	if err != nil || owner.Role != RoleAdmin {
		t.Fatalf("a verified email joins the existing account and keeps its role: %+v %v", owner, err)
	}
	if sub := linked(); sub != "admin-sub" {
		t.Fatalf("linked to %q", sub)
	}
	// Once linked, the identity is recognised by its subject alone.
	again, err := st.FindOrCreateOIDCUser(ctx, "admin-sub", "renamed@example.com", "Admin", RoleViewer, false)
	if err != nil || again.ID != owner.ID {
		t.Fatalf("a linked identity signs in by subject: %+v %v", again, err)
	}
}

// Sign-in looks accounts up in lower case, so the first administrator is
// stored that way, and migration 024 puts right the ones that were not.
func TestAdminEmailIsStoredAsItIsLookedUp(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	if err := st.EnsureAdmin(ctx, " Ops.Lead@Example.COM ", "change-me-too"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.VerifyLogin(ctx, "ops.lead@example.com", "change-me-too"); err != nil {
		t.Fatalf("the administrator cannot sign in: %v", err)
	}

	// Rows as older versions left them.
	if _, err := st.Pool.Exec(ctx, `INSERT INTO users (email, password_hash, role, org_id)
		SELECT x, password_hash, 'admin', org_id FROM users, unnest(ARRAY['Old.Admin@Example.com','Twin@Example.com','twin@example.com','John@Example.com','jOhn@example.com']) x
		WHERE email='ops.lead@example.com'`); err != nil {
		t.Fatal(err)
	}
	sql, err := migrationsFS.ReadFile("migrations/024_lowercase_emails.sql")
	if err != nil {
		t.Fatal(err)
	}
	// "John@" and "jOhn@" both need changing and would become the same
	// address. The migration must leave them, not fail: a failed migration
	// stops Argus from starting at all.
	if _, err := st.Pool.Exec(ctx, string(sql)); err != nil {
		t.Fatalf("the migration failed, and Argus would not start: %v", err)
	}
	var johns int
	if err := st.Pool.QueryRow(ctx, `SELECT count(*) FROM users WHERE email IN ('John@Example.com','jOhn@example.com')`).Scan(&johns); err != nil || johns != 2 {
		t.Fatalf("two accounts that differ only by case, neither in lower case, must both be left alone: %d %v", johns, err)
	}
	if _, err := st.VerifyLogin(ctx, "Old.Admin@example.com", "change-me-too"); err != nil {
		t.Fatalf("an administrator stored with capitals still cannot sign in: %v", err)
	}
	var twins int
	if err := st.Pool.QueryRow(ctx, `SELECT count(*) FROM users WHERE lower(email)='twin@example.com'`).Scan(&twins); err != nil || twins != 2 {
		t.Fatalf("two accounts that differ only by case must both be left alone: %d %v", twins, err)
	}
}

// The command-line reset is the only way back in for an administrator who
// has forgotten their password.
func TestResetPassword(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	if err := st.EnsureAdmin(ctx, "admin@example.com", "change-me-too"); err != nil {
		t.Fatal(err)
	}
	id, err := st.VerifyLogin(ctx, "admin@example.com", "change-me-too")
	if err != nil {
		t.Fatal(err)
	}
	session, err := st.CreateSession(ctx, id)
	if err != nil {
		t.Fatal(err)
	}

	if err := st.ResetPassword(ctx, "admin@example.com", "short"); err == nil {
		t.Fatal("a short password should be refused")
	}
	if err := st.ResetPassword(ctx, "nobody@example.com", "a-new-passphrase"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("an unknown email: %v", err)
	}
	if err := st.ResetPassword(ctx, " Admin@Example.com ", "a-new-passphrase"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.VerifyLogin(ctx, "admin@example.com", "a-new-passphrase"); err != nil {
		t.Fatalf("the new password should work: %v", err)
	}
	if _, err := st.VerifyLogin(ctx, "admin@example.com", "change-me-too"); err == nil {
		t.Fatal("the old password should not")
	}
	if _, ok, _ := st.UserIDBySession(ctx, session); ok {
		t.Fatal("existing sessions should be signed out")
	}
}
