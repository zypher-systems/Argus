package store

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// api and probe open the store at the same moment on a first boot and after
// every upgrade. Each must come up, whichever applies the migrations.
func TestConcurrentStartsMigrateOnce(t *testing.T) {
	dsn := os.Getenv("ARGUS_TEST_DB_URL")
	if dsn == "" {
		t.Skip("ARGUS_TEST_DB_URL unset")
	}
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = admin.Close(ctx) }()
	name := fmt.Sprintf("argus_migrate_%d", time.Now().UnixNano())
	if _, err := admin.Exec(ctx, `CREATE DATABASE `+pgx.Identifier{name}.Sanitize()); err != nil {
		t.Skipf("cannot create a scratch database: %v", err)
	}
	defer func() {
		_, _ = admin.Exec(ctx, `DROP DATABASE IF EXISTS `+pgx.Identifier{name}.Sanitize()+` WITH (FORCE)`)
	}()
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name

	const starts = 6
	errs := make([]error, starts)
	var wg sync.WaitGroup
	for i := range starts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			st, err := New(ctx, u.String())
			if err == nil {
				st.Close()
			}
			errs[i] = err
		}()
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Errorf("start %d of %d: %v", i+1, starts, err)
		}
	}
}
