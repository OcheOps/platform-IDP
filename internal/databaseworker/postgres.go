package databaseworker

import (
	"context"
	"errors"
	"regexp"
	"strings"
)

var postgresIdentifier = regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}$`)

// PostgresAdmin is a secret-aware adapter boundary. Implementations must make
// both operations concurrency-safe, verify Marker before changing an existing
// object, and never log Role.Password.
type PostgresAdmin interface {
	EnsureRole(context.Context, Role) error
	EnsureDatabase(context.Context, OwnedDatabase) error
}

type Role struct {
	Name, Password, Marker                         string
	Login                                          bool
	Superuser, CreateDatabase, CreateRole, Inherit bool
}

type OwnedDatabase struct {
	Name, Owner, Marker string
	RevokePublicConnect bool
}

type PostgresProvisioner struct{ admin PostgresAdmin }

func NewPostgresProvisioner(admin PostgresAdmin) *PostgresProvisioner {
	return &PostgresProvisioner{admin: admin}
}

func (provisioner *PostgresProvisioner) Ensure(ctx context.Context, database Database) error {
	if !postgresIdentifier.MatchString(database.Name) || !strings.HasPrefix(database.Name, "svc_") || !postgresIdentifier.MatchString(database.User) || !strings.HasPrefix(database.User, "usr_") || database.Password == "" {
		return errors.New("PostgreSQL service identity is invalid")
	}
	marker := "platform-idp:" + database.Name
	if err := provisioner.admin.EnsureRole(ctx, Role{Name: database.User, Password: database.Password, Marker: marker, Login: true}); err != nil {
		return errors.New("reconcile restricted PostgreSQL role")
	}
	if err := provisioner.admin.EnsureDatabase(ctx, OwnedDatabase{Name: database.Name, Owner: database.User, Marker: marker, RevokePublicConnect: true}); err != nil {
		return errors.New("reconcile isolated PostgreSQL database")
	}
	return nil
}
