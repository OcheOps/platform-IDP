package repositoryworker

import (
	"context"
	"errors"
	"fmt"
)

var ErrNoWork = errors.New("no repository work is ready")

type Work struct {
	RequestID, Organization, Name, OwnerTeam, RenderedPath string
	RepositoryID                                           int64
}

type Store interface {
	Next(context.Context) (Work, error)
	Started(context.Context, string) error
	RecordRepository(context.Context, string, int64) error
	Ready(context.Context, string) error
	Failed(context.Context, string, string, bool) error
}

type GitHub interface {
	EnsureRepository(context.Context, Work) (int64, error)
	EnsureInitialCommit(context.Context, Work, int64) error
	EnsureSettings(context.Context, Work, int64) error
	Verify(context.Context, Work, int64) error
}

type Worker struct {
	store  Store
	github GitHub
}
type transientError struct{ error }

func (transientError) Temporary() bool   { return true }
func (err transientError) Unwrap() error { return err.error }
func Transient(err error) error {
	if err == nil {
		return nil
	}
	return transientError{err}
}

func New(store Store, github GitHub) *Worker { return &Worker{store: store, github: github} }

func (worker *Worker) Reconcile(ctx context.Context) error {
	work, err := worker.store.Next(ctx)
	if err != nil {
		return err
	}
	if err := worker.store.Started(ctx, work.RequestID); err != nil {
		return fmt.Errorf("record repository start: %w", err)
	}
	repositoryID, err := worker.github.EnsureRepository(ctx, work)
	if err != nil {
		return worker.fail(ctx, work.RequestID, "github_repository_failed", err)
	}
	if repositoryID <= 0 {
		return worker.fail(ctx, work.RequestID, "github_repository_failed", errors.New("GitHub returned an invalid repository ID"))
	}
	if err := worker.store.RecordRepository(ctx, work.RequestID, repositoryID); err != nil {
		return fmt.Errorf("record GitHub repository identity: %w", err)
	}
	if err := worker.github.EnsureInitialCommit(ctx, work, repositoryID); err != nil {
		return worker.fail(ctx, work.RequestID, "github_initial_commit_failed", err)
	}
	if err := worker.github.EnsureSettings(ctx, work, repositoryID); err != nil {
		return worker.fail(ctx, work.RequestID, "github_settings_failed", err)
	}
	if err := worker.github.Verify(ctx, work, repositoryID); err != nil {
		return worker.fail(ctx, work.RequestID, "github_verification_failed", err)
	}
	if err := worker.store.Ready(ctx, work.RequestID); err != nil {
		return fmt.Errorf("record repository readiness: %w", err)
	}
	return nil
}

func (worker *Worker) fail(ctx context.Context, requestID, code string, cause error) error {
	retryable := false
	var temporary interface{ Temporary() bool }
	if errors.As(cause, &temporary) {
		retryable = temporary.Temporary()
	}
	if err := worker.store.Failed(ctx, requestID, code, retryable); err != nil {
		return errors.Join(cause, fmt.Errorf("record repository failure: %w", err))
	}
	return cause
}
