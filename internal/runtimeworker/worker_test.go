package runtimeworker_test

import (
	"context"
	"testing"

	"github.com/OcheOps/platform-IDP/internal/argocd"
	"github.com/OcheOps/platform-IDP/internal/runtimeworker"
)

func TestRuntimeWorkerRequiresMergedPRAndHealthyArgo(t *testing.T) {
	store := &fakeStore{work: runtimeworker.Work{RequestID: "req-1", ServiceName: "orders-api", PullRequestNumber: 7}}
	gh := fakeGitHub{merged: true, revision: "merge-sha"}
	argo := fakeArgo{observation: argocd.Observation{State: argocd.Healthy, Revision: "sync-sha"}}
	if err := runtimeworker.New(store, gh, argo).Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if store.state != "ready" || store.revision != "sync-sha" {
		t.Fatalf("unexpected state: %#v", store)
	}
}

type fakeStore struct {
	work            runtimeworker.Work
	state, revision string
}

func (s *fakeStore) NextRuntime(context.Context) (runtimeworker.Work, error) { return s.work, nil }
func (s *fakeStore) Ready(_ context.Context, _ string, revision string) error {
	s.state, s.revision = "ready", revision
	return nil
}
func (s *fakeStore) Failed(context.Context, string, string) error { s.state = "failed"; return nil }

type fakeGitHub struct {
	merged   bool
	revision string
}

func (g fakeGitHub) ObserveDeploymentPR(context.Context, runtimeworker.Work) (runtimeworker.GitHubObservation, error) {
	return runtimeworker.GitHubObservation{Merged: g.merged, Revision: g.revision}, nil
}

type fakeArgo struct{ observation argocd.Observation }

func (a fakeArgo) Observe(context.Context, string) (argocd.Observation, error) {
	return a.observation, nil
}
