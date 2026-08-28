package applyworker_test

import (
	"context"
	"testing"

	"github.com/OcheOps/platform-IDP/internal/applyworker"
)

func TestSuccessfulApplyTransitionsToDeploying(t *testing.T) {
	store := &fakeStore{work: applyworker.Work{RequestID: "request-1", RequestState: "approved"}}
	provider := &fakeGitHub{observation: applyworker.Observation{State: applyworker.Succeeded, MergeSHA: "merge-sha"}}
	if err := applyworker.New(store, provider).Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if store.state != "applying" {
		t.Fatalf("approved request skipped applying: %q", store.state)
	}
	store.work.RequestState = "applying"
	store.work.ApplyMergeSHA = "merge-sha"
	if err := applyworker.New(store, provider).Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if store.state != "applied" || store.mergeSHA != "merge-sha" {
		t.Fatalf("unexpected store state %q at %q", store.state, store.mergeSHA)
	}
}

func TestUnchangedApplyingObservationDoesNotWriteAgain(t *testing.T) {
	store := &fakeStore{work: applyworker.Work{RequestID: "request-1", RequestState: "applying", ApplyMergeSHA: "merge-sha"}}
	provider := &fakeGitHub{observation: applyworker.Observation{State: applyworker.Applying, MergeSHA: "merge-sha"}}
	if err := applyworker.New(store, provider).Reconcile(context.Background()); err != applyworker.ErrNoWork {
		t.Fatalf("expected no-work sentinel, got %v", err)
	}
	if store.state != "" {
		t.Fatalf("unchanged observation wrote state: %q", store.state)
	}
}

type fakeStore struct {
	work     applyworker.Work
	state    string
	mergeSHA string
	failure  string
}

func (store *fakeStore) Next(context.Context) (applyworker.Work, error) { return store.work, nil }
func (store *fakeStore) MarkApplying(_ context.Context, _ string, sha string) error {
	store.state, store.mergeSHA = "applying", sha
	return nil
}
func (store *fakeStore) Applied(_ context.Context, _ applyworker.Work, sha string) error {
	store.state, store.mergeSHA = "applied", sha
	return nil
}
func (store *fakeStore) ApplyFailed(_ context.Context, _, sha, code string) error {
	store.state, store.mergeSHA, store.failure = "failed", sha, code
	return nil
}
func (store *fakeStore) Cancelled(context.Context, string) error {
	store.state = "cancelled"
	return nil
}
func (store *fakeStore) Invalidated(context.Context, string, string) error {
	store.state = "invalidated"
	return nil
}

type fakeGitHub struct{ observation applyworker.Observation }

func (provider *fakeGitHub) ObserveApply(context.Context, applyworker.Work) (applyworker.Observation, error) {
	return provider.observation, nil
}
