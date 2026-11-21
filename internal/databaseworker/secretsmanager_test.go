package databaseworker_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager/types"

	"github.com/OcheOps/platform-IDP/internal/databaseworker"
)

func TestSecretsManagerReadsMasterAndPublishesOnlyDatabaseURL(t *testing.T) {
	client := newFakeSecretsClient()
	client.values["master"] = `{"username":"platform_admin","password":"master-password"}`
	adapter := databaseworker.NewSecretsManager(client, fixedSecretPassword("service-password"))
	master, err := adapter.MasterCredentials(context.Background(), "master")
	if err != nil {
		t.Fatal(err)
	}
	if master.Username != "platform_admin" || master.Password != "master-password" {
		t.Fatalf("unexpected master credentials: user=%q", master.Username)
	}
	password, err := adapter.PrepareServicePassword(context.Background(), "internal-password")
	if err != nil {
		t.Fatal(err)
	}
	if password != "service-password" {
		t.Fatalf("password = %q", password)
	}
	if err := adapter.PutServiceSecret(context.Background(), "service-secret", map[string]string{"DATABASE_URL": "postgres://redacted"}); err != nil {
		t.Fatal(err)
	}
	if client.values["service-secret"] != `{"DATABASE_URL":"postgres://redacted"}` {
		t.Fatalf("unexpected service secret: %s", client.values["service-secret"])
	}
}

func TestSecretsManagerReusesPreparedPassword(t *testing.T) {
	client := newFakeSecretsClient()
	adapter := databaseworker.NewSecretsManager(client, fixedSecretPassword("first-password"))
	first, err := adapter.PrepareServicePassword(context.Background(), "internal-password")
	if err != nil {
		t.Fatal(err)
	}
	second, err := databaseworker.NewSecretsManager(client, fixedSecretPassword("different-password")).PrepareServicePassword(context.Background(), "internal-password")
	if err != nil {
		t.Fatal(err)
	}
	if first != "first-password" || second != first {
		t.Fatalf("prepared password was not stable: first=%q second=%q", first, second)
	}
}

type fixedSecretPassword string

func (password fixedSecretPassword) Generate() (string, error) { return string(password), nil }

type fakeSecretsClient struct{ values map[string]string }

func newFakeSecretsClient() *fakeSecretsClient {
	return &fakeSecretsClient{values: map[string]string{}}
}
func (client *fakeSecretsClient) GetSecretValue(_ context.Context, input *secretsmanager.GetSecretValueInput, _ ...func(*secretsmanager.Options)) (*secretsmanager.GetSecretValueOutput, error) {
	value, ok := client.values[aws.ToString(input.SecretId)]
	if !ok {
		return nil, &types.ResourceNotFoundException{}
	}
	return &secretsmanager.GetSecretValueOutput{SecretString: aws.String(value)}, nil
}
func (client *fakeSecretsClient) PutSecretValue(_ context.Context, input *secretsmanager.PutSecretValueInput, _ ...func(*secretsmanager.Options)) (*secretsmanager.PutSecretValueOutput, error) {
	client.values[aws.ToString(input.SecretId)] = aws.ToString(input.SecretString)
	return &secretsmanager.PutSecretValueOutput{}, nil
}
