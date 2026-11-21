// Package databasejob launches and observes the one-shot in-cluster database
// provisioner for requests in provisioning_database. The control plane never
// holds database or Secrets Manager credentials; it only applies the Job and
// records its terminal outcome.
package databasejob

import (
	"context"
	"errors"
	"fmt"
	"net/url"

	"github.com/OcheOps/platform-IDP/internal/databaseworker"
)

var ErrNoWork = errors.New("no database Job work is ready")

type Store interface {
	PendingDatabases(context.Context) ([]databaseworker.Work, error)
	Started(context.Context, string) error
	Ready(context.Context, string) error
	Failed(context.Context, string, string) error
}

type Cluster interface {
	Apply(context.Context, string, any) error
	Get(context.Context, string, any) (bool, error)
}

type Worker struct {
	store   Store
	cluster Cluster
	config  Config
}

func New(store Store, cluster Cluster, config Config) (*Worker, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	return &Worker{store: store, cluster: cluster, config: config}, nil
}

type job struct {
	Metadata struct {
		Labels map[string]string `json:"labels"`
	} `json:"metadata"`
	Status struct {
		Conditions []struct {
			Type   string `json:"type"`
			Status string `json:"status"`
			Reason string `json:"reason"`
		} `json:"conditions"`
	} `json:"status"`
}

// Reconcile advances every pending request by at most one step. It returns
// ErrNoWork when nothing changed so the caller can back off while Jobs run.
func (worker *Worker) Reconcile(ctx context.Context) error {
	works, err := worker.store.PendingDatabases(ctx)
	if err != nil {
		return err
	}
	progressed := false
	var errs []error
	for _, work := range works {
		changed, err := worker.reconcile(ctx, work)
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return err
		}
		progressed = progressed || changed
		if err != nil {
			errs = append(errs, fmt.Errorf("request %s: %w", work.RequestID, err))
		}
	}
	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	if !progressed {
		return ErrNoWork
	}
	return nil
}

func (worker *Worker) reconcile(ctx context.Context, work databaseworker.Work) (bool, error) {
	if err := validateWork(work); err != nil {
		return true, worker.fail(ctx, work.RequestID, "database_job_invalid", err)
	}
	name := Name(work.ServiceName, work.RequestID)
	jobPath := worker.path("apis/batch/v1", "jobs", name)
	var observed job
	found, err := worker.cluster.Get(ctx, jobPath, &observed)
	if err != nil {
		return false, err
	}
	if !found {
		if err := worker.store.Started(ctx, work.RequestID); err != nil {
			return false, fmt.Errorf("record database Job start: %w", err)
		}
		// The egress policy must exist before the pod can be scheduled.
		if err := worker.cluster.Apply(ctx, worker.path("apis/networking.k8s.io/v1", "networkpolicies", name), networkPolicyManifest(worker.config, work)); err != nil {
			return false, fmt.Errorf("apply database Job network policy: %w", err)
		}
		if err := worker.cluster.Apply(ctx, jobPath, jobManifest(worker.config, work)); err != nil {
			return false, fmt.Errorf("apply database Job: %w", err)
		}
		return true, nil
	}
	if observed.Metadata.Labels["platform.idp/request-id"] != work.RequestID || observed.Metadata.Labels["app.kubernetes.io/name"] != "database-provisioner" {
		return true, worker.fail(ctx, work.RequestID, "database_job_conflict", errors.New("existing database Job belongs to another request"))
	}
	for _, condition := range observed.Status.Conditions {
		if condition.Status != "True" {
			continue
		}
		switch condition.Type {
		case "Complete":
			if err := worker.store.Ready(ctx, work.RequestID); err != nil {
				return false, fmt.Errorf("record database readiness: %w", err)
			}
			return true, nil
		case "Failed":
			code := "database_job_failed"
			if condition.Reason == "DeadlineExceeded" {
				code = "database_job_deadline_exceeded"
			}
			return true, worker.fail(ctx, work.RequestID, code, errors.New("database provisioner Job failed"))
		}
	}
	return false, nil
}

func (worker *Worker) path(group, resource, name string) string {
	return "/" + group + "/namespaces/" + url.PathEscape(worker.config.Namespace) + "/" + resource + "/" + url.PathEscape(name)
}

func (worker *Worker) fail(ctx context.Context, requestID, code string, cause error) error {
	if err := worker.store.Failed(ctx, requestID, code); err != nil {
		return errors.Join(cause, fmt.Errorf("record database failure: %w", err))
	}
	return cause
}
