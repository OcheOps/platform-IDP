package configworker

import (
	"context"
	"crypto/sha256"
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
	root, organization, repository string
	platform                       Platform
}

func NewFileStore(root, organization, repository string, platform Platform) *FileStore {
	return &FileStore{root: root, organization: organization, repository: repository, platform: platform}
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
		record, err := readRecord(filepath.Join(store.root, "requests", entry.Name()))
		if err != nil {
			return Work{}, err
		}
		state, failure := stringValue(record["configState"]), stringValue(record["failureCode"])
		attempts := integerValue(record["configAttempts"])
		if attempts >= 8 || (state == "config_failed" && !boolValue(record["configRetryable"])) {
			continue
		}
		if next := stringValue(record["configNextAttemptAt"]); next != "" {
			when, err := time.Parse(time.RFC3339Nano, next)
			if err != nil {
				return Work{}, fmt.Errorf("invalid configNextAttemptAt: %w", err)
			}
			if time.Now().Before(when) {
				continue
			}
		}
		eligible := state == "" || state == "config_pending" || state == "config_creating" || (state == "config_failed" && strings.HasPrefix(failure, "github_config_"))
		if stringValue(record["repositoryState"]) != "repository_ready" || stringValue(record["requestState"]) != "draft" || !eligible {
			continue
		}
		requestID, service := stringValue(record["requestId"]), stringValue(record["serviceName"])
		if requestID == "" || service == "" || len(record["request"]) == 0 || len(record["costEstimate"]) == 0 {
			return Work{}, errors.New("config work record is incomplete")
		}
		return Work{RequestID: requestID, Organization: store.organization, ServiceName: service, Repository: store.repository, Branch: "platform/request-" + requestID, Request: append([]byte(nil), record["request"]...), Estimate: append([]byte(nil), record["costEstimate"]...), PullRequestNumber: integerValue(record["configPullRequestNumber"]), Platform: store.platform}, nil
	}
	return Work{}, ErrNoWork
}

func (store *FileStore) Started(_ context.Context, id string) error {
	return store.update(id, func(record map[string]json.RawMessage) error {
		set(record, "configState", "config_creating")
		set(record, "configAttempts", integerValue(record["configAttempts"])+1)
		set(record, "configRetryable", false)
		set(record, "configNextAttemptAt", "")
		return nil
	})
}
func (store *FileStore) RecordPullRequest(_ context.Context, id string, number int64, head string) error {
	return store.update(id, func(record map[string]json.RawMessage) error {
		set(record, "configPullRequestNumber", number)
		set(record, "configHeadSHA", head)
		set(record, "failureCode", "")
		return nil
	})
}
func (store *FileStore) Planning(_ context.Context, id string) error {
	return store.update(id, func(record map[string]json.RawMessage) error {
		if integerValue(record["configPullRequestNumber"]) <= 0 || stringValue(record["configHeadSHA"]) == "" {
			return errors.New("planning invariants are not satisfied")
		}
		set(record, "configState", "pull_request_open")
		set(record, "requestState", "planning")
		set(record, "failureCode", "")
		set(record, "configRetryable", false)
		set(record, "configNextAttemptAt", "")
		return nil
	})
}
func (store *FileStore) Failed(_ context.Context, id, code string, retryable bool) error {
	return store.update(id, func(record map[string]json.RawMessage) error {
		set(record, "configState", "config_failed")
		set(record, "failureCode", code)
		set(record, "configRetryable", retryable)
		if retryable {
			attempt := integerValue(record["configAttempts"])
			delay := time.Duration(1<<min(int(attempt), 6)) * time.Second
			seed := sha256.Sum256([]byte(fmt.Sprintf("%s/config/%d", id, attempt)))
			delay += time.Duration(seed[0]) * delay / 512
			set(record, "configNextAttemptAt", time.Now().UTC().Add(delay).Format(time.RFC3339Nano))
		} else {
			set(record, "configNextAttemptAt", "")
		}
		return nil
	})
}
func (store *FileStore) update(id string, mutate func(map[string]json.RawMessage) error) error {
	path := filepath.Join(store.root, "requests", id+".json")
	record, err := readRecord(path)
	if err != nil {
		return err
	}
	if err := mutate(record); err != nil {
		return err
	}
	return writeRecord(path, record)
}
func readRecord(path string) (map[string]json.RawMessage, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var value map[string]json.RawMessage
	err = json.Unmarshal(data, &value)
	return value, err
}
func writeRecord(path string, value map[string]json.RawMessage) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".config-worker-*.tmp")
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
func stringValue(raw json.RawMessage) string {
	var value string
	_ = json.Unmarshal(raw, &value)
	return value
}
func integerValue(raw json.RawMessage) int64 {
	var value int64
	_ = json.Unmarshal(raw, &value)
	return value
}
func boolValue(raw json.RawMessage) bool {
	var value bool
	_ = json.Unmarshal(raw, &value)
	return value
}
func set(record map[string]json.RawMessage, key string, value any) {
	data, _ := json.Marshal(value)
	record[key] = data
}
