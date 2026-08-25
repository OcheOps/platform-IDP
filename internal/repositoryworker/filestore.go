package repositoryworker

import (
	"context"
	"crypto/rand"
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

type FileStore struct{ root, organization string }

func NewFileStore(root, organization string) *FileStore {
	return &FileStore{root: root, organization: organization}
}

func (store *FileStore) Next(ctx context.Context) (Work, error) {
	if err := ctx.Err(); err != nil {
		return Work{}, err
	}
	entries, err := os.ReadDir(filepath.Join(store.root, "requests"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Work{}, ErrNoWork
		}
		return Work{}, err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(store.root, "requests", entry.Name())
		record, err := readRecord(path)
		if err != nil {
			return Work{}, err
		}
		state := rawString(record["repositoryState"])
		failureCode := rawString(record["failureCode"])
		attempts := int(rawInt64(record["repositoryAttempts"]))
		if attempts >= 8 {
			continue
		}
		if state == "repository_failed" && !rawBool(record["repositoryRetryable"]) {
			continue
		}
		if next := rawString(record["repositoryNextAttemptAt"]); next != "" {
			when, err := time.Parse(time.RFC3339Nano, next)
			if err != nil {
				return Work{}, fmt.Errorf("invalid repositoryNextAttemptAt: %w", err)
			}
			if time.Now().Before(when) {
				continue
			}
		}
		if state != "repository_creating" && !(state == "repository_failed" && strings.HasPrefix(failureCode, "github_")) {
			continue
		}
		var request struct {
			Metadata struct {
				Owner string `json:"owner"`
			} `json:"metadata"`
		}
		if err := json.Unmarshal(record["request"], &request); err != nil {
			return Work{}, fmt.Errorf("decode desired request: %w", err)
		}
		name, requestID := rawString(record["serviceName"]), rawString(record["requestId"])
		return Work{RequestID: requestID, Organization: store.organization, Name: name, OwnerTeam: request.Metadata.Owner, RenderedPath: filepath.Join(store.root, "repositories", name), RepositoryID: rawInt64(record["githubRepositoryId"])}, nil
	}
	return Work{}, ErrNoWork
}

func (store *FileStore) Started(_ context.Context, requestID string) error {
	return store.update(requestID, func(record map[string]json.RawMessage) error {
		attempt := rawInt64(record["repositoryAttempts"]) + 1
		setRaw(record, "repositoryAttempts", attempt)
		setRaw(record, "repositoryState", "repository_creating")
		setRaw(record, "repositoryNextAttemptAt", "")
		setRaw(record, "repositoryRetryable", false)
		if attempt == 1 {
			return appendEvent(record, requestID, "repository.started", "repository", rawString(record["serviceName"]), "started", map[string]any{"attempt": attempt})
		}
		return appendEvent(record, requestID, "operation.retried", "repository", rawString(record["serviceName"]), "started", map[string]any{"attempt": attempt})
	})
}

func (store *FileStore) RecordRepository(_ context.Context, requestID string, repositoryID int64) error {
	return store.update(requestID, func(record map[string]json.RawMessage) error {
		setRaw(record, "repositoryState", "repository_creating")
		setRaw(record, "githubRepositoryId", repositoryID)
		setRaw(record, "failureCode", "")
		return nil
	})
}

func (store *FileStore) Ready(_ context.Context, requestID string) error {
	return store.update(requestID, func(record map[string]json.RawMessage) error {
		if rawInt64(record["githubRepositoryId"]) <= 0 || rawString(record["repositoryState"]) != "repository_creating" {
			return errors.New("repository readiness invariants are not satisfied")
		}
		setRaw(record, "repositoryState", "repository_ready")
		setRaw(record, "failureCode", "")
		setRaw(record, "repositoryRetryable", false)
		setRaw(record, "repositoryNextAttemptAt", "")
		return appendEvent(record, requestID, "repository.created", "repository", rawString(record["serviceName"]), "succeeded", map[string]any{"githubRepositoryId": rawInt64(record["githubRepositoryId"])})
	})
}

func (store *FileStore) Failed(_ context.Context, requestID, code string, retryable bool) error {
	return store.update(requestID, func(record map[string]json.RawMessage) error {
		setRaw(record, "repositoryState", "repository_failed")
		setRaw(record, "failureCode", code)
		setRaw(record, "repositoryRetryable", retryable)
		attempt := rawInt64(record["repositoryAttempts"])
		if retryable {
			delay := time.Duration(1<<min(int(attempt), 6)) * time.Second
			jitterSeed := sha256.Sum256([]byte(fmt.Sprintf("%s/%d", requestID, attempt)))
			delay += time.Duration(jitterSeed[0]) * delay / 512
			setRaw(record, "repositoryNextAttemptAt", time.Now().UTC().Add(delay).Format(time.RFC3339Nano))
		} else {
			setRaw(record, "repositoryNextAttemptAt", "")
		}
		return appendEvent(record, requestID, "repository.failed", "repository", rawString(record["serviceName"]), "failed", map[string]any{"failureCode": code})
	})
}

func (store *FileStore) update(requestID string, mutate func(map[string]json.RawMessage) error) error {
	path := filepath.Join(store.root, "requests", requestID+".json")
	record, err := readRecord(path)
	if err != nil {
		return err
	}
	if err := mutate(record); err != nil {
		return err
	}
	return writeRecord(path, record)
}

func appendEvent(record map[string]json.RawMessage, requestID, action, subjectType, subjectID, outcome string, metadata map[string]any) error {
	eventID, err := randomEventUUID()
	if err != nil {
		return err
	}
	event := map[string]any{"id": eventID, "occurredAt": time.Now().UTC().Format(time.RFC3339Nano), "actor": map[string]string{"type": "system", "id": "repository-worker"}, "action": action, "subject": map[string]string{"type": subjectType, "id": subjectID}, "requestId": requestID, "outcome": outcome, "metadata": metadata}
	var events []json.RawMessage
	if data := record["auditEvents"]; len(data) > 0 {
		if err := json.Unmarshal(data, &events); err != nil {
			return err
		}
	}
	encoded, err := json.Marshal(event)
	if err != nil {
		return err
	}
	events = append(events, encoded)
	setRaw(record, "auditEvents", events)
	return nil
}

func readRecord(path string) (map[string]json.RawMessage, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var record map[string]json.RawMessage
	if err := json.Unmarshal(data, &record); err != nil {
		return nil, err
	}
	return record, nil
}
func writeRecord(path string, record map[string]json.RawMessage) error {
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".worker-*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer func() { _ = os.Remove(temporaryPath) }()
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(append(data, '\n')); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, path)
}
func rawString(data json.RawMessage) string {
	var value string
	_ = json.Unmarshal(data, &value)
	return value
}
func rawInt64(data json.RawMessage) int64 {
	var value int64
	_ = json.Unmarshal(data, &value)
	return value
}
func rawBool(data json.RawMessage) bool {
	var value bool
	_ = json.Unmarshal(data, &value)
	return value
}
func setRaw(record map[string]json.RawMessage, key string, value any) {
	data, _ := json.Marshal(value)
	record[key] = data
}
func randomEventUUID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	value[6] = value[6]&0x0f | 0x40
	value[8] = value[8]&0x3f | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", value[0:4], value[4:6], value[6:8], value[8:10], value[10:16]), nil
}
