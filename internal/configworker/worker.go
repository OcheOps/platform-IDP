package configworker

import (
	"context"
	"errors"
	"fmt"
)

var ErrNoWork = errors.New("no platform-config work is ready")

type Work struct {
	RequestID, Organization, ServiceName, Repository, Branch string
	Request, Estimate                                        []byte
	PullRequestNumber                                        int64
}

type Store interface {
	Next(context.Context) (Work, error)
	Started(context.Context, string) error
	RecordPullRequest(context.Context, string, int64, string) error
	Planning(context.Context, string) error
	Failed(context.Context, string, string, bool) error
}

type GitHub interface {
	EnsureChange(context.Context, Work) (string, error)
	EnsurePullRequest(context.Context, Work, string) (int64, error)
	VerifyPullRequest(context.Context, Work, int64, string) error
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
	if err := worker.store.Started(ctx, work.RequestID); err != nil {
		return fmt.Errorf("record config change start: %w", err)
	}
	headSHA, err := worker.github.EnsureChange(ctx, work)
	if err != nil {
		return worker.fail(ctx, work.RequestID, "github_config_change_failed", err)
	}
	if headSHA == "" {
		return worker.fail(ctx, work.RequestID, "github_config_change_failed", errors.New("GitHub returned an empty config branch head"))
	}
	pr, err := worker.github.EnsurePullRequest(ctx, work, headSHA)
	if err != nil {
		return worker.fail(ctx, work.RequestID, "github_config_pull_request_failed", err)
	}
	if pr <= 0 {
		return worker.fail(ctx, work.RequestID, "github_config_pull_request_failed", errors.New("GitHub returned an invalid pull request number"))
	}
	if err := worker.store.RecordPullRequest(ctx, work.RequestID, pr, headSHA); err != nil {
		return fmt.Errorf("record config pull request identity: %w", err)
	}
	if err := worker.github.VerifyPullRequest(ctx, work, pr, headSHA); err != nil {
		return worker.fail(ctx, work.RequestID, "github_config_verification_failed", err)
	}
	if err := worker.store.Planning(ctx, work.RequestID); err != nil {
		return fmt.Errorf("record planning state: %w", err)
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
		return errors.Join(cause, fmt.Errorf("record config failure: %w", err))
	}
	return cause
}
