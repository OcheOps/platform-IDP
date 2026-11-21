package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/OcheOps/platform-IDP/internal/bootstrap"
)

func TestReadPrivateKeyRejectsBroadPermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.pem")
	if err := os.WriteFile(path, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readPrivateKey(path); err == nil {
		t.Fatal("broad private-key permissions were accepted")
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	if data, err := readPrivateKey(path); err != nil || string(data) != "secret" {
		t.Fatalf("secure key read = %q, %v", data, err)
	}
}

func TestReadPrivateKeyRejectsSymlink(t *testing.T) {
	directory := t.TempDir()
	target := filepath.Join(directory, "target.pem")
	link := filepath.Join(directory, "app.pem")
	if err := os.WriteFile(target, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := readPrivateKey(link); err == nil {
		t.Fatal("symlinked private key was accepted")
	}
}

func TestDatabaseJobWorkerConfiguration(t *testing.T) {
	installation := bootstrap.Config{InstallationID: "small-team-idp", AWS: bootstrap.AWSConfig{Region: "eu-west-1"}, Network: bootstrap.NetworkConfig{VPCCIDR: "10.42.0.0/16"}}
	environment := map[string]string{}
	getenv := func(name string) string { return environment[name] }
	if worker, err := newDatabaseJobWorker(installation, getenv); worker != nil || err != nil {
		t.Fatalf("disabled worker = %v, %v", worker, err)
	}
	environment["KUBERNETES_API_URL"] = "https://cluster.example"
	if _, err := newDatabaseJobWorker(installation, getenv); err == nil {
		t.Fatal("accepted missing token file and database host")
	}
	environment["KUBERNETES_TOKEN_FILE"] = filepath.Join(t.TempDir(), "token")
	environment["DATABASE_HOST"] = "shared.internal"
	environment["DATABASE_PROVISIONER_IMAGE"] = "registry.example/provisioner:latest"
	if _, err := newDatabaseJobWorker(installation, getenv); err == nil {
		t.Fatal("accepted mutable provisioner image")
	}
	environment["DATABASE_PROVISIONER_IMAGE"] = "registry.example/provisioner@sha256:" + strings.Repeat("b", 64)
	environment["DATABASE_PORT"] = "70000"
	if _, err := newDatabaseJobWorker(installation, getenv); err == nil {
		t.Fatal("accepted invalid database port")
	}
	delete(environment, "DATABASE_PORT")
	if worker, err := newDatabaseJobWorker(installation, getenv); worker == nil || err != nil {
		t.Fatalf("configured worker = %v, %v", worker, err)
	}
}
