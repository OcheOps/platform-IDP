package configworker_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/OcheOps/platform-IDP/internal/configworker"
)

func TestReconcileOpensAndVerifiesConfigPullRequestBeforePlanning(t *testing.T) {
	store := &recordingStore{work: configworker.Work{RequestID: "request-1", ServiceName: "orders-api"}}
	github := &recordingGitHub{calls: &store.calls}
	if err := configworker.New(store, github).Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := []string{"started", "ensure-change", "ensure-pr:head-sha", "record-pr:17:head-sha", "verify-pr:17:head-sha", "planning"}
	if !reflect.DeepEqual(store.calls, want) {
		t.Fatalf("calls=%#v want=%#v", store.calls, want)
	}
}

type recordingStore struct {
	work  configworker.Work
	calls []string
}

func (s *recordingStore) Next(context.Context) (configworker.Work, error) { return s.work, nil }
func (s *recordingStore) Started(context.Context, string) error {
	s.calls = append(s.calls, "started")
	return nil
}
func (s *recordingStore) RecordPullRequest(_ context.Context, _ string, n int64, sha string) error {
	s.calls = append(s.calls, "record-pr:17:"+sha)
	return nil
}
func (s *recordingStore) Planning(context.Context, string) error {
	s.calls = append(s.calls, "planning")
	return nil
}
func (s *recordingStore) Failed(_ context.Context, _ string, code string, _ bool) error {
	s.calls = append(s.calls, "failed:"+code)
	return nil
}

type recordingGitHub struct{ calls *[]string }

func (g *recordingGitHub) EnsureChange(context.Context, configworker.Work) (string, error) {
	*g.calls = append(*g.calls, "ensure-change")
	return "head-sha", nil
}
func (g *recordingGitHub) EnsurePullRequest(_ context.Context, _ configworker.Work, sha string) (int64, error) {
	*g.calls = append(*g.calls, "ensure-pr:"+sha)
	return 17, nil
}
func (g *recordingGitHub) VerifyPullRequest(_ context.Context, _ configworker.Work, n int64, sha string) error {
	*g.calls = append(*g.calls, "verify-pr:17:"+sha)
	return nil
}
