package main

import "testing"

func TestWorkFromEnvironment(t *testing.T) {
	values := map[string]string{
		"REQUEST_ID": "request-1", "DATABASE_NAME": "svc_orders", "DATABASE_USER": "usr_orders", "DATABASE_HOST": "shared.internal",
		"DATABASE_PORT": "5432", "MASTER_SECRET_NAME": "platform/idp/shared/postgres-master", "PASSWORD_SECRET_NAME": "platform/idp/orders/staging/internal/database-password",
		"SERVICE_SECRET_NAME": "platform/idp/orders/staging/database", "AWS_REGION": "us-east-1",
	}
	work, region, err := workFromEnvironment(func(name string) string { return values[name] })
	if err != nil {
		t.Fatal(err)
	}
	if work.DatabaseName != "svc_orders" || work.PasswordSecretName == work.SecretName || region != "us-east-1" {
		t.Fatalf("unexpected work: %#v region=%q", work, region)
	}
}

func TestWorkFromEnvironmentRejectsInvalidPort(t *testing.T) {
	if _, _, err := workFromEnvironment(func(string) string { return "not-a-port" }); err == nil {
		t.Fatal("invalid port was accepted")
	}
}

func TestWorkFromEnvironmentRejectsMissingIdentityBeforeAWSInitialization(t *testing.T) {
	values := map[string]string{"DATABASE_PORT": "5432", "AWS_REGION": "us-east-1"}
	if _, _, err := workFromEnvironment(func(name string) string { return values[name] }); err == nil {
		t.Fatal("incomplete work was accepted")
	}
}
