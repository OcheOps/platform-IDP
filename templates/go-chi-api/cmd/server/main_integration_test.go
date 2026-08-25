package main

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestServerProcessEndpointsAndGracefulShutdown(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "service")
	build := exec.Command("go", "build", "-buildvcs=false", "-o", binary, ".")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build server: %v\n%s", err, output)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		if errors.Is(err, syscall.EPERM) {
			t.Skip("sandbox does not permit local sockets")
		}
		t.Fatal(err)
	}
	address := listener.Addr().String()
	_ = listener.Close()

	var logs bytes.Buffer
	command := exec.Command(binary)
	command.Env = append(os.Environ(), "HTTP_ADDRESS="+address)
	if databaseURL := os.Getenv("TEST_DATABASE_URL"); databaseURL != "" {
		command.Env = append(command.Env, "DATABASE_URL="+databaseURL)
	}
	command.Stdout = &logs
	command.Stderr = &logs
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if command.Process != nil {
			_ = command.Process.Kill()
		}
	})

	client := &http.Client{Timeout: time.Second}
	for _, path := range []string{"/health/live", "/health/ready", "/metrics"} {
		url := fmt.Sprintf("http://%s%s", address, path)
		deadline := time.Now().Add(5 * time.Second)
		for {
			response, requestErr := client.Get(url)
			if requestErr == nil {
				_ = response.Body.Close()
				if response.StatusCode < 200 || response.StatusCode >= 300 {
					t.Fatalf("%s returned %d", path, response.StatusCode)
				}
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s did not become ready: %v\n%s", path, requestErr, logs.String())
			}
			time.Sleep(25 * time.Millisecond)
		}
	}

	started := time.Now()
	if err := command.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	wait := make(chan error, 1)
	go func() { wait <- command.Wait() }()
	select {
	case err := <-wait:
		if err != nil {
			t.Fatalf("server shutdown failed: %v\n%s", err, logs.String())
		}
		if time.Since(started) > 5*time.Second {
			t.Fatalf("server exceeded graceful shutdown test deadline")
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("server did not stop after SIGTERM\n%s", logs.String())
	}
}
