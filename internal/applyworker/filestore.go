package applyworker

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type FileStore struct {
	root, organization, repository, installationID, accountID, region string
}

func NewFileStore(root, organization, repository, installationID, accountID, region string) *FileStore {
	return &FileStore{root, organization, repository, installationID, accountID, region}
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
		state := text(record["requestState"])
		if state != "approved" && state != "applying" && state != "apply_failed" {
			continue
		}
		var request struct {
			Spec struct {
				Database struct {
					Engine string `json:"engine"`
				} `json:"database"`
			} `json:"spec"`
		}
		if err := json.Unmarshal(record["request"], &request); err != nil {
			return Work{}, errors.New("decode apply service request")
		}
		work := Work{
			RequestID: text(record["requestId"]), ServiceName: text(record["serviceName"]), Organization: store.organization,
			Repository: store.repository, HeadSHA: text(record["configHeadSHA"]), PlanDigest: text(record["planDigest"]),
			PullRequestNumber: number(record["configPullRequestNumber"]), InstallationID: store.installationID,
			AWSAccountID: store.accountID, AWSRegion: store.region, Database: request.Spec.Database.Engine == "postgres",
			RequestState: state, ApplyMergeSHA: text(record["applyMergeSHA"]), FailureCode: text(record["failureCode"]),
		}
		if work.RequestID == "" || work.ServiceName == "" || work.HeadSHA == "" || work.PlanDigest == "" || work.PullRequestNumber <= 0 {
			return Work{}, errors.New("apply work record is incomplete")
		}
		return work, nil
	}
	return Work{}, ErrNoWork
}

func (store *FileStore) MarkApplying(_ context.Context, id, mergeSHA string) error {
	return store.update(id, func(record map[string]json.RawMessage) error {
		if text(record["requestState"]) != "applying" {
			set(record, "requestState", "applying")
			set(record, "applyMergeSHA", mergeSHA)
			set(record, "failureCode", "")
			return event(record, id, "apply.started", "succeeded", map[string]any{"mergeSHA": mergeSHA, "headSHA": text(record["configHeadSHA"]), "planDigest": text(record["planDigest"])})
		}
		return nil
	})
}

func (store *FileStore) Applied(_ context.Context, work Work, mergeSHA string) error {
	return store.update(work.RequestID, func(record map[string]json.RawMessage) error {
		resources := map[string]string{
			"canonicalId":      work.InstallationID + "/" + work.ServiceName + "/staging",
			"ecrRepository":    "platform/" + work.ServiceName,
			"ecrRepositoryUrl": work.AWSAccountID + ".dkr.ecr." + work.AWSRegion + ".amazonaws.com/platform/" + work.ServiceName,
		}
		if work.Database {
			resources["databaseSecretName"] = "platform/" + work.InstallationID + "/" + work.ServiceName + "/staging/database"
		}
		set(record, "requestState", "deploying")
		set(record, "applyMergeSHA", mergeSHA)
		set(record, "resourceIdentities", resources)
		set(record, "failureCode", "")
		return event(record, work.RequestID, "apply.completed", "succeeded", map[string]any{"mergeSHA": mergeSHA, "headSHA": work.HeadSHA, "planDigest": work.PlanDigest, "resources": resources})
	})
}

func (store *FileStore) Cancelled(_ context.Context, id string) error {
	return store.update(id, func(record map[string]json.RawMessage) error {
		set(record, "requestState", "cancelled")
		set(record, "failureCode", "")
		return event(record, id, "apply.cancelled", "succeeded", map[string]any{"headSHA": text(record["configHeadSHA"])})
	})
}

func (store *FileStore) Invalidated(_ context.Context, id, headSHA string) error {
	return store.update(id, func(record map[string]json.RawMessage) error {
		if headSHA == "" {
			return errors.New("replacement configuration head is required")
		}
		set(record, "requestState", "planning")
		set(record, "configHeadSHA", headSHA)
		set(record, "planDigest", "")
		set(record, "engineeringApprover", "")
		set(record, "platformApprover", "")
		set(record, "failureCode", "approval_head_changed")
		return event(record, id, "approval.invalidated", "succeeded", map[string]any{"headSHA": headSHA})
	})
}

func (store *FileStore) ApplyFailed(_ context.Context, id, mergeSHA, code string) error {
	return store.update(id, func(record map[string]json.RawMessage) error {
		set(record, "requestState", "apply_failed")
		set(record, "applyMergeSHA", mergeSHA)
		set(record, "failureCode", code)
		return event(record, id, "apply.failed", "failed", map[string]any{"failureCode": code, "mergeSHA": mergeSHA, "planDigest": text(record["planDigest"])})
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
	file, err := os.CreateTemp(filepath.Dir(path), ".apply-worker-*.tmp")
	if err != nil {
		return err
	}
	name := file.Name()
	defer os.Remove(name)
	if err := file.Chmod(0o600); err != nil {
		file.Close()
		return err
	}
	if _, err := file.Write(append(data, '\n')); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}

func read(path string) (map[string]json.RawMessage, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var record map[string]json.RawMessage
	err = json.Unmarshal(data, &record)
	return record, err
}
func text(value json.RawMessage) string {
	var result string
	_ = json.Unmarshal(value, &result)
	return result
}
func number(value json.RawMessage) int64 {
	var result int64
	_ = json.Unmarshal(value, &result)
	return result
}
func set(record map[string]json.RawMessage, key string, value any) {
	data, _ := json.Marshal(value)
	record[key] = data
}
func event(record map[string]json.RawMessage, id, action, outcome string, metadata map[string]any) error {
	operationKey := id + "/apply/" + text(record["configHeadSHA"])
	metadata["operationKey"] = operationKey
	set(record, "applyOperationKey", operationKey)
	var events []json.RawMessage
	if len(record["auditEvents"]) > 0 {
		if err := json.Unmarshal(record["auditEvents"], &events); err != nil {
			return err
		}
	}
	for _, rawEvent := range events {
		var existing struct {
			Action, Outcome string
			Metadata        map[string]any
		}
		if json.Unmarshal(rawEvent, &existing) == nil && existing.Action == action && existing.Outcome == outcome && existing.Metadata["operationKey"] == operationKey {
			return nil
		}
	}
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return err
	}
	raw[6] = raw[6]&15 | 64
	raw[8] = raw[8]&63 | 128
	eventID := fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", raw[0:4], raw[4:6], raw[6:8], raw[8:10], raw[10:16])
	value := map[string]any{"id": eventID, "occurredAt": time.Now().UTC().Format(time.RFC3339Nano), "actor": map[string]string{"type": "system", "id": "apply-worker"}, "action": action, "subject": map[string]string{"type": "request", "id": id}, "requestId": id, "outcome": outcome, "metadata": metadata}
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	events = append(events, encoded)
	set(record, "auditEvents", events)
	return nil
}
