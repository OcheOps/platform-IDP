package deployworker_test

import (
	"context"
	"strings"
	"testing"

	"github.com/OcheOps/platform-IDP/internal/deployworker"
)

func TestDeploymentWorkerRendersValuesAndOpensGitOpsPullRequest(t *testing.T) {
	store := &fakeStore{work: deployworker.Work{RequestID: "request-1", ServiceName: "orders-api", Owner: "orders-team", Environment: "staging", BaseDomain: "example.com", Template: "go-chi-api", TemplateVersion: "v0.1.0", CPU: "250m", Memory: "256Mi", MinReplicas: 1, MaxReplicas: 3, TargetCPU: 70, Database: true, DatabaseSecretName: "platform/idp/orders-api/staging/database"}}
	github := &fakeGitHub{observation: deployworker.Observation{ImageReady: true, ImageRepository: "123456789012.dkr.ecr.eu-west-1.amazonaws.com/platform/orders-api", ImageDigest: "sha256:" + strings.Repeat("a", 64)}}
	if err := deployworker.New(store, github).Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if store.pr != 42 || store.state != "ready" || !strings.Contains(string(github.values), `"digest": "sha256:`) {
		t.Fatalf("unexpected deployment state: %#v values=%s", store, github.values)
	}
}

type fakeStore struct {
	work  deployworker.Work
	state string
	pr    int64
}

func (s *fakeStore) Next(context.Context) (deployworker.Work, error) { return s.work, nil }
func (s *fakeStore) DeploymentStarted(context.Context, string) error { s.state = "started"; return nil }
func (s *fakeStore) RecordPullRequest(_ context.Context, _ string, pr int64) error {
	s.pr = pr
	return nil
}
func (s *fakeStore) Ready(context.Context, string) error          { s.state = "ready"; return nil }
func (s *fakeStore) Failed(context.Context, string, string) error { s.state = "failed"; return nil }

type fakeGitHub struct {
	observation deployworker.Observation
	values      []byte
}

func (g *fakeGitHub) ObserveImage(context.Context, deployworker.Work) (deployworker.Observation, error) {
	return g.observation, nil
}
func (g *fakeGitHub) EnsureDeploymentChange(_ context.Context, _ deployworker.Work, values []byte) (int64, error) {
	g.values = values
	return 42, nil
}
func (g *fakeGitHub) VerifyDeploymentChange(context.Context, deployworker.Work, int64, []byte) error {
	return nil
}
