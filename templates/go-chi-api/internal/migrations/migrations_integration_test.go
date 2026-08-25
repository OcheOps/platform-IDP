package migrations

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

func TestRunAgainstPostgres(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}

	ctx := context.Background()
	connection, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatalf("connect to test database: %v", err)
	}
	t.Cleanup(func() { _ = connection.Close(ctx) })

	reset := func() {
		if _, resetErr := connection.Exec(ctx, "DROP TABLE IF EXISTS example_items, schema_migrations"); resetErr != nil {
			t.Fatalf("reset test database: %v", resetErr)
		}
	}
	reset()
	t.Cleanup(reset)

	if err := Run(ctx, connection); err != nil {
		t.Fatalf("first migration run: %v", err)
	}
	if err := Run(ctx, connection); err != nil {
		t.Fatalf("second migration run: %v", err)
	}

	var applied int
	if err := connection.QueryRow(ctx, "SELECT count(*) FROM schema_migrations").Scan(&applied); err != nil {
		t.Fatalf("count applied migrations: %v", err)
	}
	if applied != 1 {
		t.Fatalf("applied migrations = %d, want 1", applied)
	}

	if _, err := connection.Exec(ctx, "UPDATE schema_migrations SET checksum = 'tampered'"); err != nil {
		t.Fatalf("tamper with migration checksum: %v", err)
	}
	if err := Run(ctx, connection); err == nil || !strings.Contains(err.Error(), "changed after it was applied") {
		t.Fatalf("Run() after checksum tampering = %v, want checksum mismatch", err)
	}
}
