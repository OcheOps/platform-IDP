package migrations

import "testing"

func TestMigrationChecksumDetectsEdits(t *testing.T) {
	original := migrationChecksum([]byte("create table example(id int);"))
	edited := migrationChecksum([]byte("create table example(id bigint);"))
	if original == edited {
		t.Fatal("migration edit did not change checksum")
	}
	if len(original) != 64 {
		t.Fatalf("checksum length = %d, want 64", len(original))
	}
}
