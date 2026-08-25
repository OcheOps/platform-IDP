package main

import (
	"os"
	"path/filepath"
	"testing"
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
