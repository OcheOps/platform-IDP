package applyworker

import (
	"context"
	"errors"
)

var ErrNoWork = errors.New("no apply work is ready")

type Work struct {
	RequestID, ServiceName, Organization, Repository, HeadSHA, PlanDigest, InstallationID, AWSAccountID, AWSRegion string
	RequestState, ApplyMergeSHA, FailureCode                                                                       string
	PullRequestNumber                                                                                              int64
	Database                                                                                                       bool
}

type Observation struct {
	State, MergeSHA, HeadSHA, FailureCode string
}

const (
	Pending     = "pending"
	Applying    = "applying"
	Succeeded   = "succeeded"
	Failed      = "failed"
	Cancelled   = "cancelled"
	Invalidated = "invalidated"
)

type Store interface {
	Next(context.Context) (Work, error)
	MarkApplying(context.Context, string, string) error
	Applied(context.Context, Work, string) error
	ApplyFailed(context.Context, string, string, string) error
	Cancelled(context.Context, string) error
	Invalidated(context.Context, string, string) error
}

type GitHub interface {
	ObserveApply(context.Context, Work) (Observation, error)
}

type Worker struct {
	store  Store
	github GitHub
}

func New(store Store, github GitHub) *Worker { return &Worker{store: store, github: github} }

func (worker *Worker) Reconcile(ctx context.Context) error {
	work, err := worker.store.Next(ctx)
	if err != nil {
		return err
	}
	observation, err := worker.github.ObserveApply(ctx, work)
	if err != nil {
		return err
	}
	if (work.RequestState == "approved" || work.RequestState == "apply_failed") && observation.MergeSHA != "" && (observation.State == Applying || observation.State == Succeeded) {
		return worker.store.MarkApplying(ctx, work.RequestID, observation.MergeSHA)
	}
	switch observation.State {
	case Pending:
		return ErrNoWork
	case Applying:
		if work.RequestState == "applying" && work.ApplyMergeSHA == observation.MergeSHA {
			return ErrNoWork
		}
		return worker.store.MarkApplying(ctx, work.RequestID, observation.MergeSHA)
	case Succeeded:
		return worker.store.Applied(ctx, work, observation.MergeSHA)
	case Failed:
		if observation.FailureCode == "" {
			return errors.New("apply failure code is required")
		}
		if work.RequestState == "apply_failed" && work.ApplyMergeSHA == observation.MergeSHA && work.FailureCode == observation.FailureCode {
			return ErrNoWork
		}
		return worker.store.ApplyFailed(ctx, work.RequestID, observation.MergeSHA, observation.FailureCode)
	case Cancelled:
		return worker.store.Cancelled(ctx, work.RequestID)
	case Invalidated:
		return worker.store.Invalidated(ctx, work.RequestID, observation.HeadSHA)
	default:
		return errors.New("unknown apply observation state")
	}
}
