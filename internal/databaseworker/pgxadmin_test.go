package databaseworker_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/OcheOps/platform-IDP/internal/databaseworker"
)

func TestPGXAdminCreatesMarkedRestrictedAndIsolatedResources(t *testing.T) {
	session := &fakePGSession{}
	admin := databaseworker.NewPGXAdmin(session)
	role := databaseworker.Role{Name: "usr_orders", Password: "not-logged", Marker: "platform-idp:svc_orders", Login: true}
	if err := admin.EnsureRole(context.Background(), role); err != nil {
		t.Fatal(err)
	}
	database := databaseworker.OwnedDatabase{Name: "svc_orders", Owner: role.Name, Marker: role.Marker, RevokePublicConnect: true}
	if err := admin.EnsureDatabase(context.Background(), database); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(session.execs, "\n")
	for _, want := range []string{"pg_try_advisory_lock", `CREATE ROLE "usr_orders"`, "LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT", `CREATE DATABASE "svc_orders" OWNER "usr_orders"`, `REVOKE CONNECT ON DATABASE "svc_orders" FROM PUBLIC`, `GRANT CONNECT ON DATABASE "svc_orders" TO "usr_orders"`, "pg_advisory_unlock"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %q in:\n%s", want, joined)
		}
	}
	if strings.Contains(joined, role.Password) {
		t.Fatal("password entered generic statement log boundary")
	}
	if !session.closed {
		t.Fatal("database connection was not closed")
	}
}

func TestPGXAdminRejectsUnmanagedCollision(t *testing.T) {
	session := &fakePGSession{roleMarker: "someone-else"}
	err := databaseworker.NewPGXAdmin(session).EnsureRole(context.Background(), databaseworker.Role{Name: "usr_orders", Password: "secret", Marker: "platform-idp:svc_orders", Login: true})
	if err == nil {
		t.Fatal("unmanaged role collision was accepted")
	}
}

func TestPGXAdminAdoptsOnlyUnmarkedDatabaseWithExpectedMarkedOwner(t *testing.T) {
	session := &fakePGSession{databaseExists: true, adoptable: true}
	admin := databaseworker.NewPGXAdmin(session)
	role := databaseworker.Role{Name: "usr_orders", Password: "secret", Marker: "platform-idp:svc_orders", Login: true}
	if err := admin.EnsureRole(context.Background(), role); err != nil {
		t.Fatal(err)
	}
	if err := admin.EnsureDatabase(context.Background(), databaseworker.OwnedDatabase{Name: "svc_orders", Owner: role.Name, Marker: role.Marker, RevokePublicConnect: true}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(session.execs, "\n"), `COMMENT ON DATABASE "svc_orders" IS 'platform-idp:svc_orders'`) {
		t.Fatal("adopted database was not marked")
	}
}

type fakePGSession struct {
	roleMarker, databaseMarker string
	databaseExists             bool
	adoptable                  bool
	execs                      []string
	closed                     bool
}

func (session *fakePGSession) QueryRow(_ context.Context, query string, args ...any) pgx.Row {
	session.execs = append(session.execs, query)
	switch {
	case strings.Contains(query, "pg_try_advisory_lock"):
		return fakePGRow{value: true}
	case strings.Contains(query, "SELECT EXISTS"):
		return fakePGRow{value: session.adoptable}
	case strings.Contains(query, "pg_roles"):
		if session.roleMarker == "" {
			return fakePGRow{err: pgx.ErrNoRows}
		}
		return fakePGRow{value: session.roleMarker}
	case strings.Contains(query, "pg_database"):
		if !session.databaseExists {
			return fakePGRow{err: pgx.ErrNoRows}
		}
		return fakePGRow{value: session.databaseMarker}
	default:
		return fakePGRow{value: ""}
	}
}
func (session *fakePGSession) Exec(_ context.Context, sql string, _ ...any) (pgconn.CommandTag, error) {
	session.execs = append(session.execs, sql)
	return pgconn.NewCommandTag("OK"), nil
}
func (session *fakePGSession) Close(context.Context) error                           { session.closed = true; return nil }
func (session *fakePGSession) SetRolePassword(context.Context, string, string) error { return nil }

type fakePGRow struct {
	value any
	err   error
}

func (row fakePGRow) Scan(dest ...any) error {
	if row.err != nil {
		return row.err
	}
	if len(dest) != 1 {
		return errors.New("unexpected scan")
	}
	switch target := dest[0].(type) {
	case *string:
		*target = row.value.(string)
	case *bool:
		*target = row.value.(bool)
	default:
		return errors.New("unexpected scan target")
	}
	return nil
}
