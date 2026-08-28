package planworker

import (
	"context"
	"errors"
	"fmt"
)

var ErrNoWork = errors.New("no plan work is ready")

type State string

const (
	Pending   State = "pending"
	Succeeded State = "succeeded"
	Failed    State = "failed"
)

type Work struct {
	RequestID, Organization, Repository, HeadSHA, EstimateDigest string
	PullRequestNumber                                            int64
}
type Observation struct {
	State                   State
	PlanDigest, FailureCode string
}
type Store interface {
	Next(context.Context) (Work, error)
	AwaitingApproval(context.Context, string, string) error
	PlanFailed(context.Context, string, string) error
}
type GitHub interface {
	ObservePlan(context.Context, Work) (Observation, error)
}
type Worker struct {
	store  Store
	github GitHub
}

func New(store Store, github GitHub) *Worker { return &Worker{store, github} }
func (worker *Worker) Reconcile(ctx context.Context) error {
	work, err := worker.store.Next(ctx)
	if err != nil {
		return err
	}
	observed, err := worker.github.ObservePlan(ctx, work)
	if err != nil {
		return err
	}
	switch observed.State {
	case Pending:
		return ErrNoWork
	case Failed:
		code := observed.FailureCode
		if code == "" {
			code = "plan_checks_failed"
		}
		return worker.store.PlanFailed(ctx, work.RequestID, code)
	case Succeeded:
		if observed.PlanDigest == "" {
			return errors.New("successful plan is missing its digest")
		}
		return worker.store.AwaitingApproval(ctx, work.RequestID, observed.PlanDigest)
	default:
		return fmt.Errorf("unknown plan observation state %q", observed.State)
	}
}
