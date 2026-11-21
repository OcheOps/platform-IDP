package databaseworker

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
	"sync"
	"time"
)

// FileStore is the single-process development store. A replicated runtime must
// replace it with a store that claims work using compare-and-swap or leases.
type FileStore struct {
	root, host, masterSecretName string
	port                         int
	mu                           sync.Mutex
}

func NewFileStore(root, host string, port int, masterSecretName string) *FileStore {
	return &FileStore{root: root, host: host, port: port, masterSecretName: masterSecretName}
}

func (store *FileStore) NextDatabase(ctx context.Context) (Work, error) {
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
		state := recordText(record, "requestState")
		if state != "provisioning_database" && state != "database_failed" {
			continue
		}
		var resources struct {
			DatabaseName, DatabaseUser, DatabaseSecretName, DatabasePasswordSecretName string
		}
		if err := json.Unmarshal(record["resourceIdentities"], &resources); err != nil {
			return Work{}, errors.New("decode database resource identities")
		}
		work := Work{
			RequestID: recordText(record, "requestId"), DatabaseName: resources.DatabaseName, DatabaseUser: resources.DatabaseUser,
			DatabaseHost: store.host, DatabasePort: store.port, MasterSecretName: store.masterSecretName, SecretName: resources.DatabaseSecretName,
			PasswordSecretName: resources.DatabasePasswordSecretName,
		}
		if err := validateWork(work); err != nil {
			return Work{}, err
		}
		return work, nil
	}
	return Work{}, ErrNoWork
}

func (store *FileStore) Started(_ context.Context, id string) error {
	return store.update(id, func(record map[string]json.RawMessage) error {
		if recordText(record, "databaseState") == "ready" {
			return nil
		}
		setRecord(record, "requestState", "provisioning_database")
		setRecord(record, "databaseState", "provisioning")
		setRecord(record, "failureCode", "")
		return nil
	})
}

func (store *FileStore) Ready(_ context.Context, id string) error {
	return store.update(id, func(record map[string]json.RawMessage) error {
		if recordText(record, "databaseState") == "ready" {
			return nil
		}
		setRecord(record, "requestState", "deploying")
		setRecord(record, "databaseState", "ready")
		setRecord(record, "failureCode", "")
		return appendDatabaseEvent(record, id, "database.provisioned", "succeeded", "")
	})
}

func (store *FileStore) Failed(_ context.Context, id, code string) error {
	return store.update(id, func(record map[string]json.RawMessage) error {
		if recordText(record, "databaseState") == "ready" {
			return nil
		}
		setRecord(record, "requestState", "database_failed")
		setRecord(record, "databaseState", "failed")
		setRecord(record, "failureCode", code)
		return appendDatabaseEvent(record, id, "database.provision_failed", "failed", code)
	})
}

func (store *FileStore) update(id string, mutate func(map[string]json.RawMessage) error) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	path := filepath.Join(store.root, "requests", id+".json")
	record, err := readRecord(path)
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
	file, err := os.CreateTemp(filepath.Dir(path), ".database-worker-*.tmp")
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

func readRecord(path string) (map[string]json.RawMessage, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var record map[string]json.RawMessage
	err = json.Unmarshal(data, &record)
	return record, err
}

func recordText(record map[string]json.RawMessage, key string) string {
	var value string
	_ = json.Unmarshal(record[key], &value)
	return value
}

func setRecord(record map[string]json.RawMessage, key string, value any) {
	data, _ := json.Marshal(value)
	record[key] = data
}

func appendDatabaseEvent(record map[string]json.RawMessage, id, action, outcome, code string) error {
	var events []json.RawMessage
	if len(record["auditEvents"]) > 0 {
		if err := json.Unmarshal(record["auditEvents"], &events); err != nil {
			return err
		}
	}
	for _, raw := range events {
		var event struct{ Action, Outcome string }
		if json.Unmarshal(raw, &event) == nil && event.Action == action && event.Outcome == outcome {
			return nil
		}
	}
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return err
	}
	random[6] = random[6]&15 | 64
	random[8] = random[8]&63 | 128
	eventID := fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", random[0:4], random[4:6], random[6:8], random[8:10], random[10:16])
	metadata := map[string]string{"operationKey": id + "/database"}
	if code != "" {
		metadata["failureCode"] = code
	}
	value := map[string]any{
		"id": eventID, "occurredAt": time.Now().UTC().Format(time.RFC3339Nano), "actor": map[string]string{"type": "system", "id": "database-worker"},
		"action": action, "subject": map[string]string{"type": "request", "id": id}, "requestId": id, "outcome": outcome, "metadata": metadata,
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	events = append(events, encoded)
	setRecord(record, "auditEvents", events)
	return nil
}
