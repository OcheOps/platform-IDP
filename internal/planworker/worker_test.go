package planworker_test

import (
	"context"
	"github.com/OcheOps/platform-IDP/internal/planworker"
	"testing"
)

func TestSuccessfulPlanMovesToAwaitingApproval(t *testing.T) {
	store := &store{work: planworker.Work{RequestID: "request-1"}}
	worker := planworker.New(store, provider{planworker.Observation{State: planworker.Succeeded, PlanDigest: "sha256:plan"}})
	if err := worker.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if store.state != "awaiting_approval" || store.digest != "sha256:plan" {
		t.Fatalf("state=%s digest=%s", store.state, store.digest)
	}
}

type store struct {
	work          planworker.Work
	state, digest string
}

func (s *store) Next(context.Context) (planworker.Work, error) { return s.work, nil }
func (s *store) AwaitingApproval(_ context.Context, _ string, digest string) error {
	s.state = "awaiting_approval"
	s.digest = digest
	return nil
}
func (s *store) PlanFailed(_ context.Context, _ string, code string) error {
	s.state = "plan_failed:" + code
	return nil
}

type provider struct{ result planworker.Observation }

func (p provider) ObservePlan(context.Context, planworker.Work) (planworker.Observation, error) {
	return p.result, nil
}
