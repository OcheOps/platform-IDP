package repositoryworker_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/OcheOps/platform-IDP/internal/repositoryworker"
)

func TestReconcileRecordsIdentityBeforePublishingAndOnlyThenBecomesReady(t *testing.T) {
	store := &recordingStore{work: repositoryworker.Work{RequestID: "request-1", Organization: "acme", Name: "orders-api", OwnerTeam: "orders-team", RenderedPath: "/rendered/orders-api"}}
	github := &recordingGitHub{calls: &store.calls}
	worker := repositoryworker.New(store, github)
	if err := worker.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := []string{"started", "ensure-repository", "record-repository:42", "ensure-initial-commit", "ensure-settings", "verify", "ready"}
	if !reflect.DeepEqual(store.calls, want) {
		t.Fatalf("calls = %#v, want %#v", store.calls, want)
	}
}

func TestReconcileRetainsRepositoryIdentityWhenLaterStepFails(t *testing.T) {
	store := &recordingStore{work: repositoryworker.Work{RequestID: "request-1", Organization: "acme", Name: "orders-api", OwnerTeam: "orders-team", RenderedPath: "/rendered/orders-api"}}
	github := &recordingGitHub{calls: &store.calls, publishErr: errors.New("temporary GitHub failure")}
	err := repositoryworker.New(store, github).Reconcile(context.Background())
	if err == nil {
		t.Fatal("expected reconciliation failure")
	}
	want := []string{"started", "ensure-repository", "record-repository:42", "ensure-initial-commit", "failed:github_initial_commit_failed"}
	if !reflect.DeepEqual(store.calls, want) {
		t.Fatalf("calls = %#v, want %#v", store.calls, want)
	}
	if store.repositoryID != 42 {
		t.Fatalf("repository ID was not retained: %d", store.repositoryID)
	}
}

type recordingStore struct {
	work         repositoryworker.Work
	calls        []string
	repositoryID int64
}

func (store *recordingStore) Next(context.Context) (repositoryworker.Work, error) {
	return store.work, nil
}
func (store *recordingStore) Started(context.Context, string) error {
	store.calls = append(store.calls, "started")
	return nil
}
func (store *recordingStore) RecordRepository(_ context.Context, _ string, id int64) error {
	store.repositoryID = id
	store.calls = append(store.calls, "record-repository:42")
	return nil
}
func (store *recordingStore) Ready(context.Context, string) error {
	store.calls = append(store.calls, "ready")
	return nil
}
func (store *recordingStore) Failed(_ context.Context, _ string, code string, _ bool) error {
	store.calls = append(store.calls, "failed:"+code)
	return nil
}

type recordingGitHub struct {
	calls      *[]string
	publishErr error
}

func (github *recordingGitHub) EnsureRepository(context.Context, repositoryworker.Work) (int64, error) {
	*github.calls = append(*github.calls, "ensure-repository")
	return 42, nil
}
func (github *recordingGitHub) EnsureInitialCommit(context.Context, repositoryworker.Work, int64) error {
	*github.calls = append(*github.calls, "ensure-initial-commit")
	return github.publishErr
}
func (github *recordingGitHub) EnsureSettings(context.Context, repositoryworker.Work, int64) error {
	*github.calls = append(*github.calls, "ensure-settings")
	return nil
}
func (github *recordingGitHub) Verify(context.Context, repositoryworker.Work, int64) error {
	*github.calls = append(*github.calls, "verify")
	return nil
}
