package databaseworker

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager/types"
)

type SecretsManagerClient interface {
	GetSecretValue(context.Context, *secretsmanager.GetSecretValueInput, ...func(*secretsmanager.Options)) (*secretsmanager.GetSecretValueOutput, error)
	PutSecretValue(context.Context, *secretsmanager.PutSecretValueInput, ...func(*secretsmanager.Options)) (*secretsmanager.PutSecretValueOutput, error)
}

type SecretPasswordGenerator interface{ Generate() (string, error) }

type SecretsManager struct {
	client   SecretsManagerClient
	password SecretPasswordGenerator
}

func NewSecretsManager(client SecretsManagerClient, password SecretPasswordGenerator) *SecretsManager {
	return &SecretsManager{client: client, password: password}
}

func (manager *SecretsManager) MasterCredentials(ctx context.Context, name string) (MasterCredentials, error) {
	value, err := manager.get(ctx, name)
	if err != nil {
		return MasterCredentials{}, errors.New("read database master secret")
	}
	var credentials MasterCredentials
	if json.Unmarshal([]byte(value), &credentials) != nil || credentials.Username == "" || credentials.Password == "" {
		return MasterCredentials{}, errors.New("database master secret is invalid")
	}
	return credentials, nil
}

func (manager *SecretsManager) PrepareServicePassword(ctx context.Context, name string) (string, error) {
	if value, err := manager.get(ctx, name); err == nil {
		return decodePassword(value)
	} else if !isNotFound(err) {
		return "", errors.New("read prepared database password")
	}
	password, err := manager.password.Generate()
	if err != nil || password == "" {
		return "", errors.New("generate database password")
	}
	encoded, _ := json.Marshal(map[string]string{"password": password})
	token := sha256.Sum256([]byte("platform-idp/database-password/" + name))
	_, putErr := manager.client.PutSecretValue(ctx, &secretsmanager.PutSecretValueInput{
		SecretId: aws.String(name), SecretString: aws.String(string(encoded)), ClientRequestToken: aws.String(hex.EncodeToString(token[:])),
	})
	if putErr == nil {
		return password, nil
	}
	value, getErr := manager.get(ctx, name)
	if getErr != nil {
		return "", errors.New("persist prepared database password")
	}
	return decodePassword(value)
}

func (manager *SecretsManager) PutServiceSecret(ctx context.Context, name string, value map[string]string) error {
	if len(value) != 1 || value["DATABASE_URL"] == "" {
		return errors.New("service database secret must contain only DATABASE_URL")
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return errors.New("encode service database secret")
	}
	if current, getErr := manager.get(ctx, name); getErr == nil && current == string(encoded) {
		return nil
	}
	if _, err := manager.client.PutSecretValue(ctx, &secretsmanager.PutSecretValueInput{SecretId: aws.String(name), SecretString: aws.String(string(encoded))}); err != nil {
		return errors.New("publish service database secret")
	}
	return nil
}

func (manager *SecretsManager) get(ctx context.Context, name string) (string, error) {
	output, err := manager.client.GetSecretValue(ctx, &secretsmanager.GetSecretValueInput{SecretId: aws.String(name)})
	if err != nil {
		return "", err
	}
	if output.SecretString == nil {
		return "", errors.New("binary secrets are not supported")
	}
	return aws.ToString(output.SecretString), nil
}

func decodePassword(value string) (string, error) {
	var stored struct {
		Password string `json:"password"`
	}
	if json.Unmarshal([]byte(value), &stored) != nil || stored.Password == "" {
		return "", errors.New("prepared database password is invalid")
	}
	return stored.Password, nil
}

func isNotFound(err error) bool {
	var notFound *types.ResourceNotFoundException
	return errors.As(err, &notFound)
}

type CryptoSecretPasswordGenerator struct{}

func (CryptoSecretPasswordGenerator) Generate() (string, error) {
	var value [32]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value[:]), nil
}
