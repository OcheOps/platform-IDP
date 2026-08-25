package main

import (
	"net/http"
	"testing"
)

func TestDevelopmentAuthenticatorRequiresTokenAndActor(t *testing.T) {
	authenticate := developmentAuthenticator("12345678901234567890123456789012")
	request, _ := http.NewRequest(http.MethodPost, "http://localhost/v1/service-requests", nil)
	if _, err := authenticate(request); err == nil {
		t.Fatal("missing token was accepted")
	}
	request.Header.Set("Authorization", "Bearer wrong")
	request.Header.Set("X-Platform-Actor", "ocheops")
	if _, err := authenticate(request); err == nil {
		t.Fatal("wrong token was accepted")
	}
	request.Header.Set("Authorization", "Bearer 12345678901234567890123456789012")
	actor, err := authenticate(request)
	if err != nil || actor != "ocheops" {
		t.Fatalf("valid authentication = %q, %v", actor, err)
	}
}

func TestDevelopmentServerRejectsNonLoopbackAddresses(t *testing.T) {
	for _, address := range []string{"0.0.0.0:8090", "10.0.0.1:8090", ":8090"} {
		if err := requireLoopback(address); err == nil {
			t.Errorf("%s was accepted", address)
		}
	}
	if err := requireLoopback("127.0.0.1:8090"); err != nil {
		t.Fatalf("loopback rejected: %v", err)
	}
}
