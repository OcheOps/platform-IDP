package runtimeworker

import (
	"context"
	"errors"
	"fmt"

	"github.com/OcheOps/platform-IDP/internal/argocd"
)

var ErrNoWork = errors.New("no runtime deployment work is ready")

type Work struct {
	RequestID, ServiceName, Organization, Repository string
	PullRequestNumber                                int64
}

type GitHubObservation struct {
	Merged   bool
	Revision string
}

type Store interface {
	NextRuntime(context.Context) (Work, error)
	Ready(context.Context, string, string) error
	Failed(context.Context, string, string) error
}

type GitHub interface {
	ObserveDeploymentPR(context.Context, Work) (GitHubObservation, error)
}
type Argo interface {
	Observe(context.Context, string) (argocd.Observation, error)
}

type Worker struct {
	store  Store
	github GitHub
	argo   Argo
}

func New(store Store, github GitHub, argo Argo) *Worker {
	return &Worker{store: store, github: github, argo: argo}
}

func (worker *Worker) Reconcile(ctx context.Context) error {
	work, err := worker.store.NextRuntime(ctx)
	if err != nil {
		return err
	}
	pr, err := worker.github.ObserveDeploymentPR(ctx, work)
	if err != nil {
		return err
	}
	if !pr.Merged {
		return ErrNoWork
	}
	application := work.ServiceName + "-staging"
	observed, err := worker.argo.Observe(ctx, application)
	if err != nil {
		return err
	}
	if observed.State == argocd.Pending {
		return ErrNoWork
	}
	if observed.State == argocd.Degraded {
		return worker.fail(ctx, work.RequestID, "argocd_degraded", errors.New(observed.Message))
	}
	if observed.State != argocd.Healthy {
		return ErrNoWork
	}
	revision := observed.Revision
	if revision == "" {
		revision = pr.Revision
	}
	if err := worker.store.Ready(ctx, work.RequestID, revision); err != nil {
		return fmt.Errorf("record runtime readiness: %w", err)
	}
	return nil
}

func (worker *Worker) fail(ctx context.Context, id, code string, cause error) error {
	if err := worker.store.Failed(ctx, id, code); err != nil {
		return errors.Join(cause, fmt.Errorf("record runtime failure: %w", err))
	}
	return cause
}
