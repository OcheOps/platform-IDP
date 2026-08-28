package approvalworker

import (
	"context"
	"errors"
)

var ErrNoWork = errors.New("no approval work is ready")

type Work struct {
	RequestID, Requester, Organization, Repository, HeadSHA, PlanDigest, EngineeringOwnerTeam, PlatformOwnerTeam string
	PullRequestNumber                                                                                            int64
}
type Evidence struct{ EngineeringApprover, PlatformApprover string }
type Store interface {
	Next(context.Context) (Work, error)
	Approved(context.Context, string, Evidence) error
}
type GitHub interface {
	ObserveApprovals(context.Context, Work) (Evidence, bool, error)
	PublishApproval(context.Context, Work, Evidence) error
}
type Worker struct {
	store  Store
	github GitHub
}

func New(store Store, github GitHub) *Worker { return &Worker{store, github} }
func (w *Worker) Reconcile(ctx context.Context) error {
	work, err := w.store.Next(ctx)
	if err != nil {
		return err
	}
	evidence, ready, err := w.github.ObserveApprovals(ctx, work)
	if err != nil {
		return err
	}
	if !ready {
		return ErrNoWork
	}
	if evidence.EngineeringApprover == "" || evidence.PlatformApprover == "" || evidence.EngineeringApprover == evidence.PlatformApprover {
		return errors.New("approval evidence is not independent")
	}
	if err := w.github.PublishApproval(ctx, work, evidence); err != nil {
		return err
	}
	return w.store.Approved(ctx, work.RequestID, evidence)
}
