package databaseworker

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type PGSession interface {
	QueryRow(context.Context, string, ...any) pgx.Row
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	SetRolePassword(context.Context, string, string) error
	Close(context.Context) error
}

type PGXAdmin struct {
	session PGSession
	locked  bool
	marker  string
}

func NewPGXAdmin(session PGSession) *PGXAdmin { return &PGXAdmin{session: session} }

func (admin *PGXAdmin) EnsureRole(ctx context.Context, role Role) error {
	if err := admin.lock(ctx, role.Marker); err != nil {
		return err
	}
	failed := true
	defer func() {
		if failed {
			_ = admin.unlock(ctx)
			_ = admin.session.Close(ctx)
		}
	}()
	if _, err := admin.session.Exec(ctx, "BEGIN"); err != nil {
		return errors.New("begin PostgreSQL role reconciliation")
	}
	transactionOpen := true
	defer func() {
		if transactionOpen {
			_, _ = admin.session.Exec(context.Background(), "ROLLBACK")
		}
	}()
	marker, exists, err := admin.roleMarker(ctx, role.Name)
	if err != nil {
		return err
	}
	if exists && marker != role.Marker {
		return errors.New("PostgreSQL role is not managed by this service")
	}
	identifier := pgx.Identifier{role.Name}.Sanitize()
	if !exists {
		if _, err := admin.session.Exec(ctx, "CREATE ROLE "+identifier); err != nil {
			return errors.New("create PostgreSQL role")
		}
	}
	privileges := " NOLOGIN"
	if role.Login {
		privileges = " LOGIN"
	}
	if role.Superuser {
		privileges += " SUPERUSER"
	} else {
		privileges += " NOSUPERUSER"
	}
	if role.CreateDatabase {
		privileges += " CREATEDB"
	} else {
		privileges += " NOCREATEDB"
	}
	if role.CreateRole {
		privileges += " CREATEROLE"
	} else {
		privileges += " NOCREATEROLE"
	}
	if role.Inherit {
		privileges += " INHERIT"
	} else {
		privileges += " NOINHERIT"
	}
	if _, err := admin.session.Exec(ctx, "ALTER ROLE "+identifier+privileges); err != nil {
		return errors.New("restrict PostgreSQL role")
	}
	if err := admin.session.SetRolePassword(ctx, role.Name, role.Password); err != nil {
		return errors.New("set PostgreSQL role password")
	}
	comment := "COMMENT ON ROLE " + identifier + " IS " + quoteSQLLiteral(role.Marker)
	if _, err := admin.session.Exec(ctx, comment); err != nil {
		return errors.New("mark PostgreSQL role ownership")
	}
	if _, err := admin.session.Exec(ctx, "COMMIT"); err != nil {
		return errors.New("commit PostgreSQL role reconciliation")
	}
	transactionOpen = false
	failed = false
	return nil
}

func (admin *PGXAdmin) EnsureDatabase(ctx context.Context, database OwnedDatabase) (result error) {
	defer func() {
		if err := admin.unlock(ctx); result == nil && err != nil {
			result = err
		}
		_ = admin.session.Close(ctx)
	}()
	marker, exists, err := admin.databaseMarker(ctx, database.Name)
	if err != nil {
		return err
	}
	if exists && marker != database.Marker {
		adoptable, adoptErr := admin.databaseIsAdoptable(ctx, database)
		if adoptErr != nil {
			return adoptErr
		}
		if marker != "" || !adoptable {
			return errors.New("PostgreSQL database is not managed by this service")
		}
	}
	name, owner := pgx.Identifier{database.Name}.Sanitize(), pgx.Identifier{database.Owner}.Sanitize()
	if !exists {
		if _, err := admin.session.Exec(ctx, "CREATE DATABASE "+name+" OWNER "+owner); err != nil {
			return errors.New("create PostgreSQL database")
		}
	}
	if _, err := admin.session.Exec(ctx, "ALTER DATABASE "+name+" OWNER TO "+owner); err != nil {
		return errors.New("reconcile PostgreSQL database owner")
	}
	if _, err := admin.session.Exec(ctx, "COMMENT ON DATABASE "+name+" IS "+quoteSQLLiteral(database.Marker)); err != nil {
		return errors.New("mark PostgreSQL database ownership")
	}
	if database.RevokePublicConnect {
		if _, err := admin.session.Exec(ctx, "REVOKE CONNECT ON DATABASE "+name+" FROM PUBLIC"); err != nil {
			return errors.New("revoke public database access")
		}
		if _, err := admin.session.Exec(ctx, "GRANT CONNECT ON DATABASE "+name+" TO "+owner); err != nil {
			return errors.New("grant service database access")
		}
	}
	return nil
}

func (admin *PGXAdmin) lock(ctx context.Context, marker string) error {
	for {
		var acquired bool
		if err := admin.session.QueryRow(ctx, "SELECT pg_try_advisory_lock(hashtextextended($1, 0))", marker).Scan(&acquired); err != nil {
			return errors.New("lock PostgreSQL service identity")
		}
		if acquired {
			break
		}
		timer := time.NewTimer(250 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return errors.New("database identity lock deadline exceeded")
		case <-timer.C:
		}
	}
	admin.locked = true
	admin.marker = marker
	return nil
}
func (admin *PGXAdmin) unlock(ctx context.Context) error {
	if !admin.locked {
		return nil
	}
	admin.locked = false
	if _, err := admin.session.Exec(ctx, "SELECT pg_advisory_unlock(hashtextextended($1, 0))", admin.marker); err != nil {
		return errors.New("unlock PostgreSQL service identity")
	}
	admin.marker = ""
	return nil
}

func (admin *PGXAdmin) roleMarker(ctx context.Context, name string) (string, bool, error) {
	var marker string
	err := admin.session.QueryRow(ctx, "SELECT COALESCE(shobj_description(oid, 'pg_authid'), '') FROM pg_roles WHERE rolname=$1", name).Scan(&marker)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, errors.New("inspect PostgreSQL role ownership")
	}
	return marker, true, nil
}
func (admin *PGXAdmin) databaseMarker(ctx context.Context, name string) (string, bool, error) {
	var marker string
	err := admin.session.QueryRow(ctx, "SELECT COALESCE(shobj_description(oid, 'pg_database'), '') FROM pg_database WHERE datname=$1", name).Scan(&marker)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, errors.New("inspect PostgreSQL database ownership")
	}
	return marker, true, nil
}
func (admin *PGXAdmin) databaseIsAdoptable(ctx context.Context, database OwnedDatabase) (bool, error) {
	var adoptable bool
	err := admin.session.QueryRow(ctx, `SELECT EXISTS (
  SELECT 1 FROM pg_database database
  JOIN pg_roles owner ON owner.oid = database.datdba
  WHERE database.datname = $1 AND owner.rolname = $2
    AND shobj_description(owner.oid, 'pg_authid') = $3
)`, database.Name, database.Owner, database.Marker).Scan(&adoptable)
	if err != nil {
		return false, errors.New("verify PostgreSQL database adoption")
	}
	return adoptable, nil
}
func quoteSQLLiteral(value string) string { return "'" + strings.ReplaceAll(value, "'", "''") + "'" }

type pgxSession struct{ connection *pgx.Conn }

func (session *pgxSession) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	return session.connection.QueryRow(ctx, sql, args...)
}
func (session *pgxSession) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	return session.connection.Exec(ctx, sql, args...)
}
func (session *pgxSession) Close(ctx context.Context) error { return session.connection.Close(ctx) }
func (session *pgxSession) SetRolePassword(ctx context.Context, name, password string) error {
	if _, err := session.connection.Exec(ctx, "SELECT set_config('platform_idp.role_password', $1, true)", password); err != nil {
		return err
	}
	statement := "DO $platform_idp$ BEGIN EXECUTE format('ALTER ROLE %I PASSWORD %L', " + quoteSQLLiteral(name) + ", current_setting('platform_idp.role_password')); END $platform_idp$"
	if _, err := session.connection.Exec(ctx, statement); err != nil {
		return err
	}
	return nil
}

type PGXConnector struct{}

func (PGXConnector) Connect(ctx context.Context, credentials MasterCredentials, host string, port int) (Provisioner, error) {
	connection := &url.URL{Scheme: "postgres", User: url.UserPassword(credentials.Username, credentials.Password), Host: net.JoinHostPort(host, strconv.Itoa(port)), Path: "/postgres", RawQuery: "sslmode=require"}
	session, err := pgx.Connect(ctx, connection.String())
	if err != nil {
		return nil, fmt.Errorf("connect to PostgreSQL: %w", err)
	}
	return NewPostgresProvisioner(NewPGXAdmin(&pgxSession{connection: session})), nil
}
