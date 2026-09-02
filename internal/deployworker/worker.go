package deployworker

import (
	"context"
	"errors"
	"fmt"

	"github.com/OcheOps/platform-IDP/internal/gitops"
)

var ErrNoWork = errors.New("no deployment work is ready")

type Work struct {
	RequestID, ServiceName, Owner, Organization, Repository, Environment, BaseDomain string
	AWSAccountID, AWSRegion                                                          string
	Template, TemplateVersion, CPU, Memory                                           string
	MinReplicas, MaxReplicas, TargetCPU                                              int
	Database, ImageReady                                                             bool
	ImageRepository, ImageDigest, DatabaseSecretName                                 string
	PullRequestNumber                                                                int64
}

type Observation struct {
	ImageReady      bool
	ImageRepository string
	ImageDigest     string
}

type Store interface {
	Next(context.Context) (Work, error)
	DeploymentStarted(context.Context, string) error
	RecordPullRequest(context.Context, string, int64) error
	Ready(context.Context, string) error
	Failed(context.Context, string, string) error
}

type GitHub interface {
	ObserveImage(context.Context, Work) (Observation, error)
	EnsureDeploymentChange(context.Context, Work, []byte) (int64, error)
	VerifyDeploymentChange(context.Context, Work, int64, []byte) error
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
	observation, err := worker.github.ObserveImage(ctx, work)
	if err != nil {
		return err
	}
	if !observation.ImageReady {
		return ErrNoWork
	}
	work.ImageRepository, work.ImageDigest = observation.ImageRepository, observation.ImageDigest
	values, err := gitops.RenderValues(gitops.Work{Service: work.ServiceName, Owner: work.Owner, Template: work.Template, TemplateVersion: work.TemplateVersion, Environment: work.Environment, BaseDomain: work.BaseDomain, RequestID: work.RequestID, ImageRepository: work.ImageRepository, ImageDigest: work.ImageDigest, CPU: work.CPU, Memory: work.Memory, MinReplicas: work.MinReplicas, MaxReplicas: work.MaxReplicas, TargetCPU: work.TargetCPU, Database: work.Database, DatabaseSecretName: work.DatabaseSecretName})
	if err != nil {
		return worker.fail(ctx, work.RequestID, "deployment_values_invalid", err)
	}
	if err := worker.store.DeploymentStarted(ctx, work.RequestID); err != nil {
		return fmt.Errorf("record deployment start: %w", err)
	}
	if work.PullRequestNumber > 0 {
		if err := worker.github.VerifyDeploymentChange(ctx, work, work.PullRequestNumber, values); err != nil {
			return worker.fail(ctx, work.RequestID, "github_deployment_verification_failed", err)
		}
	} else {
		pr, err := worker.github.EnsureDeploymentChange(ctx, work, values)
		if err != nil {
			return worker.fail(ctx, work.RequestID, "github_deployment_pull_request_failed", err)
		}
		if pr <= 0 {
			return worker.fail(ctx, work.RequestID, "github_deployment_pull_request_failed", errors.New("GitHub returned an invalid deployment pull request number"))
		}
		if err := worker.store.RecordPullRequest(ctx, work.RequestID, pr); err != nil {
			return fmt.Errorf("record deployment pull request: %w", err)
		}
		work.PullRequestNumber = pr
		if err := worker.github.VerifyDeploymentChange(ctx, work, pr, values); err != nil {
			return worker.fail(ctx, work.RequestID, "github_deployment_verification_failed", err)
		}
	}
	return worker.store.Ready(ctx, work.RequestID)
}

func (worker *Worker) fail(ctx context.Context, id, code string, cause error) error {
	if err := worker.store.Failed(ctx, id, code); err != nil {
		return errors.Join(cause, fmt.Errorf("record deployment failure: %w", err))
	}
	return cause
}
