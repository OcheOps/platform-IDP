package deployworker

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/OcheOps/platform-IDP/internal/runtimeworker"
)

type FileStore struct{ root, organization, repository, baseDomain, accountID, region string }

func NewFileStore(root, organization, repository, baseDomain, accountID, region string) *FileStore {
	return &FileStore{root, organization, repository, baseDomain, accountID, region}
}

func (store *FileStore) Next(ctx context.Context) (Work, error) {
	if err := ctx.Err(); err != nil {
		return Work{}, err
	}
	entries, err := os.ReadDir(filepath.Join(store.root, "requests"))
	if errors.Is(err, os.ErrNotExist) {
		return Work{}, ErrNoWork
	}
	if err != nil {
		return Work{}, err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		record, err := read(filepath.Join(store.root, "requests", entry.Name()))
		if err != nil {
			return Work{}, err
		}
		if text(record["requestState"]) != "deploying" {
			continue
		}
		if state := text(record["deploymentState"]); state == "ready" || state == "failed" || state == "awaiting_merge" {
			continue
		}
		var request struct {
			Metadata struct {
				Owner string `json:"owner"`
			} `json:"metadata"`
			Spec struct {
				Template struct {
					ID      string `json:"id"`
					Version string `json:"version"`
				} `json:"template"`
				Capacity struct {
					CPU         string `json:"cpu"`
					Memory      string `json:"memory"`
					MinReplicas int    `json:"minReplicas"`
					MaxReplicas int    `json:"maxReplicas"`
				} `json:"capacity"`
				Database struct {
					Engine string `json:"engine"`
				} `json:"database"`
			} `json:"spec"`
		}
		if err := json.Unmarshal(record["request"], &request); err != nil {
			return Work{}, errors.New("decode deployment service request")
		}
		work := Work{RequestID: text(record["requestId"]), ServiceName: text(record["serviceName"]), Owner: request.Metadata.Owner, Organization: store.organization, Repository: store.repository, Environment: "staging", BaseDomain: store.baseDomain, AWSAccountID: store.accountID, AWSRegion: store.region, Template: request.Spec.Template.ID, TemplateVersion: request.Spec.Template.Version, CPU: request.Spec.Capacity.CPU, Memory: request.Spec.Capacity.Memory, MinReplicas: request.Spec.Capacity.MinReplicas, MaxReplicas: request.Spec.Capacity.MaxReplicas, TargetCPU: 70, Database: request.Spec.Database.Engine == "postgres", DatabaseSecretName: text(record["databaseSecretName"]), PullRequestNumber: number(record["deploymentPullRequestNumber"]), DeploymentState: text(record["deploymentState"])}
		if work.RequestID == "" || work.ServiceName == "" || work.Owner == "" {
			return Work{}, errors.New("deployment work record is incomplete")
		}
		return work, nil
	}
	return Work{}, ErrNoWork
}

func (store *FileStore) DeploymentStarted(_ context.Context, id string) error {
	return store.update(id, func(r map[string]json.RawMessage) error {
		set(r, "deploymentState", "creating")
		set(r, "failureCode", "")
		return nil
	})
}
func (store *FileStore) RecordPullRequest(_ context.Context, id string, pr int64) error {
	return store.update(id, func(r map[string]json.RawMessage) error { set(r, "deploymentPullRequestNumber", pr); return nil })
}
func (store *FileStore) AwaitingMerge(_ context.Context, id string) error {
	return store.update(id, func(r map[string]json.RawMessage) error {
		set(r, "deploymentState", "awaiting_merge")
		set(r, "failureCode", "")
		return nil
	})
}
func (store *FileStore) NextRuntime(ctx context.Context) (runtimeworker.Work, error) {
	if err := ctx.Err(); err != nil {
		return runtimeworker.Work{}, err
	}
	entries, err := os.ReadDir(filepath.Join(store.root, "requests"))
	if errors.Is(err, os.ErrNotExist) {
		return runtimeworker.Work{}, runtimeworker.ErrNoWork
	}
	if err != nil {
		return runtimeworker.Work{}, err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		record, err := read(filepath.Join(store.root, "requests", entry.Name()))
		if err != nil {
			return runtimeworker.Work{}, err
		}
		if text(record["requestState"]) != "deploying" || text(record["deploymentState"]) != "awaiting_merge" {
			continue
		}
		return runtimeworker.Work{RequestID: text(record["requestId"]), ServiceName: text(record["serviceName"]), Organization: store.organization, Repository: store.repository, PullRequestNumber: number(record["deploymentPullRequestNumber"])}, nil
	}
	return runtimeworker.Work{}, runtimeworker.ErrNoWork
}
func (store *FileStore) Ready(_ context.Context, id, revision string) error {
	return store.update(id, func(r map[string]json.RawMessage) error {
		set(r, "requestState", "ready")
		set(r, "deploymentState", "ready")
		set(r, "deploymentRevision", revision)
		set(r, "failureCode", "")
		return nil
	})
}
func (store *FileStore) Failed(_ context.Context, id, code string) error {
	return store.update(id, func(r map[string]json.RawMessage) error {
		set(r, "deploymentState", "failed")
		set(r, "failureCode", code)
		return nil
	})
}
func (store *FileStore) update(id string, mutate func(map[string]json.RawMessage) error) error {
	path := filepath.Join(store.root, "requests", id+".json")
	record, err := read(path)
	if err != nil {
		return err
	}
	if err := mutate(record); err != nil {
		return err
	}
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".deploy-worker-*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.Write(append(data, '\n')); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, path)
}
func read(path string) (map[string]json.RawMessage, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var r map[string]json.RawMessage
	err = json.Unmarshal(data, &r)
	return r, err
}
func text(v json.RawMessage) string                     { var x string; _ = json.Unmarshal(v, &x); return x }
func number(v json.RawMessage) int64                    { var x int64; _ = json.Unmarshal(v, &x); return x }
func set(r map[string]json.RawMessage, k string, v any) { data, _ := json.Marshal(v); r[k] = data }
