package databaseworker_test

import (
	"context"
	"testing"

	"github.com/OcheOps/platform-IDP/internal/databaseworker"
)

func TestPostgresProvisionerReconcilesRestrictedRoleAndIsolatedDatabase(t *testing.T) {
	admin := &fakePostgresAdmin{}
	err := databaseworker.NewPostgresProvisioner(admin).Ensure(context.Background(), databaseworker.Database{
		Name: "svc_orders_api_8c7a25e2", User: "usr_orders_api_8c7a25e2", Password: "secret-value",
	})
	if err != nil {
		t.Fatal(err)
	}
	if admin.role.Name != "usr_orders_api_8c7a25e2" || admin.role.Password != "secret-value" || admin.role.Marker != "platform-idp:svc_orders_api_8c7a25e2" || !admin.role.Login || admin.role.Superuser || admin.role.CreateDatabase || admin.role.CreateRole || admin.role.Inherit {
		t.Fatalf("unexpected role identity: name=%q marker=%q", admin.role.Name, admin.role.Marker)
	}
	if admin.database.Name != "svc_orders_api_8c7a25e2" || admin.database.Owner != admin.role.Name || admin.database.Marker != admin.role.Marker || !admin.database.RevokePublicConnect {
		t.Fatalf("unexpected owned database: %#v", admin.database)
	}
}

func TestPostgresProvisionerReconcilesOnEveryRetry(t *testing.T) {
	admin := &fakePostgresAdmin{}
	provisioner := databaseworker.NewPostgresProvisioner(admin)
	input := databaseworker.Database{Name: "svc_orders_api_8c7a25e2", User: "usr_orders_api_8c7a25e2", Password: "secret-value"}
	for range 2 {
		if err := provisioner.Ensure(context.Background(), input); err != nil {
			t.Fatal(err)
		}
	}
	if admin.roleCalls != 2 || admin.databaseCalls != 2 {
		t.Fatalf("retry did not reconcile both resources: role=%d database=%d", admin.roleCalls, admin.databaseCalls)
	}
}

func TestPostgresProvisionerRejectsUnsafeOrPrivilegedIdentities(t *testing.T) {
	for _, input := range []databaseworker.Database{
		{Name: `svc_orders";drop`, User: "usr_orders", Password: "secret-value"},
		{Name: "postgres", User: "platform_admin", Password: "secret-value"},
	} {
		if err := databaseworker.NewPostgresProvisioner(&fakePostgresAdmin{}).Ensure(context.Background(), input); err == nil {
			t.Fatalf("unsafe identity was accepted: name=%q user=%q", input.Name, input.User)
		}
	}
}

type fakePostgresAdmin struct {
	role                     databaseworker.Role
	database                 databaseworker.OwnedDatabase
	roleCalls, databaseCalls int
}

func (admin *fakePostgresAdmin) EnsureRole(_ context.Context, role databaseworker.Role) error {
	admin.role, admin.roleCalls = role, admin.roleCalls+1
	return nil
}
func (admin *fakePostgresAdmin) EnsureDatabase(_ context.Context, database databaseworker.OwnedDatabase) error {
	admin.database, admin.databaseCalls = database, admin.databaseCalls+1
	return nil
}
