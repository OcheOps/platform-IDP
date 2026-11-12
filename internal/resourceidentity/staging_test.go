package resourceidentity_test

import (
	"testing"

	"github.com/OcheOps/platform-IDP/internal/resourceidentity"
)

func TestStagingDatabaseResourcesAreDeterministicAndAWSBound(t *testing.T) {
	resources, err := resourceidentity.Staging("small-team-idp", "orders-api", "123456789012", true)
	if err != nil {
		t.Fatal(err)
	}
	if resources.CanonicalID != "small-team-idp/orders-api/staging" || resources.ECRRepository != "platform/orders-api" {
		t.Fatalf("unexpected base resources: %#v", resources)
	}
	if resources.DatabaseSecretName != "platform/small-team-idp/orders-api/staging/database" {
		t.Fatalf("unexpected database secret: %#v", resources)
	}
	if resources.SecretStoreRoleName != "small-team-idp-orders-api-staging-secrets-8c7a25e2" {
		t.Fatalf("unexpected secret role name: %#v", resources)
	}
	if resources.SecretStoreRoleARN != "arn:aws:iam::123456789012:role/small-team-idp-orders-api-staging-secrets-8c7a25e2" {
		t.Fatalf("unexpected secret role ARN: %#v", resources)
	}
}

func TestStagingWithoutDatabaseOmitsDatabaseResources(t *testing.T) {
	resources, err := resourceidentity.Staging("small-team-idp", "orders-api", "123456789012", false)
	if err != nil {
		t.Fatal(err)
	}
	if resources.DatabaseSecretName != "" || resources.SecretStoreRoleName != "" || resources.SecretStoreRoleARN != "" {
		t.Fatalf("database-free service received database resources: %#v", resources)
	}
}

func TestStagingRejectsInvalidIdentity(t *testing.T) {
	if _, err := resourceidentity.Staging("small-team-idp", "Orders API", "123", true); err == nil {
		t.Fatal("invalid resource identity was accepted")
	}
	if _, err := resourceidentity.Staging("small-team-idp", "a", "123456789012", true); err == nil {
		t.Fatal("one-character service identity was accepted")
	}
}

func TestStagingSecretRoleTruncationPreservesCanonicalHash(t *testing.T) {
	resources, err := resourceidentity.Staging("long-platform-installation-id", "extraordinarily-long-orders-service-api", "123456789012", true)
	if err != nil {
		t.Fatal(err)
	}
	const want = "long-platform-installation-id-extraordinarily-long-orde-4c659e2e"
	if resources.SecretStoreRoleName != want || len(resources.SecretStoreRoleName) != 64 {
		t.Fatalf("secret role name = %q, want %q", resources.SecretStoreRoleName, want)
	}
}
