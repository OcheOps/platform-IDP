package planworker

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

type FileStore struct{ root, organization, repository string }

func NewFileStore(root, organization, repository string) *FileStore {
	return &FileStore{root, organization, repository}
}
func (s *FileStore) Next(ctx context.Context) (Work, error) {
	if err := ctx.Err(); err != nil {
		return Work{}, err
	}
	entries, err := os.ReadDir(filepath.Join(s.root, "requests"))
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
		record, err := read(filepath.Join(s.root, "requests", entry.Name()))
		if err != nil {
			return Work{}, err
		}
		if text(record["requestState"]) != "planning" || text(record["configState"]) != "pull_request_open" {
			continue
		}
		var estimate struct {
			Digest string `json:"digest"`
		}
		if json.Unmarshal(record["costEstimate"], &estimate) != nil {
			return Work{}, errors.New("decode stored estimate")
		}
		work := Work{RequestID: text(record["requestId"]), Organization: s.organization, Repository: s.repository, HeadSHA: text(record["configHeadSHA"]), EstimateDigest: estimate.Digest, PullRequestNumber: number(record["configPullRequestNumber"])}
		if work.RequestID == "" || work.HeadSHA == "" || work.EstimateDigest == "" || work.PullRequestNumber <= 0 {
			return Work{}, errors.New("plan work record is incomplete")
		}
		return work, nil
	}
	return Work{}, ErrNoWork
}
func (s *FileStore) AwaitingApproval(_ context.Context, id, digest string) error {
	return s.update(id, func(r map[string]json.RawMessage) error {
		set(r, "requestState", "awaiting_approval")
		set(r, "planDigest", digest)
		set(r, "failureCode", "")
		return event(r, id, "plan.completed", "succeeded", map[string]any{"planDigest": digest, "headSHA": text(r["configHeadSHA"])})
	})
}
func (s *FileStore) PlanFailed(_ context.Context, id, code string) error {
	return s.update(id, func(r map[string]json.RawMessage) error {
		set(r, "requestState", "plan_failed")
		set(r, "failureCode", code)
		return event(r, id, "plan.failed", "failed", map[string]any{"failureCode": code, "headSHA": text(r["configHeadSHA"])})
	})
}
func (s *FileStore) update(id string, mutate func(map[string]json.RawMessage) error) error {
	path := filepath.Join(s.root, "requests", id+".json")
	r, err := read(path)
	if err != nil {
		return err
	}
	if err := mutate(r); err != nil {
		return err
	}
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".plan-worker-*.tmp")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if err := f.Chmod(0o600); err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(append(data, '\n')); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
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
func event(r map[string]json.RawMessage, id, action, outcome string, metadata map[string]any) error {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return err
	}
	raw[6] = raw[6]&0x0f | 0x40
	raw[8] = raw[8]&0x3f | 0x80
	eid := fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", raw[0:4], raw[4:6], raw[6:8], raw[8:10], raw[10:16])
	value := map[string]any{"id": eid, "occurredAt": time.Now().UTC().Format(time.RFC3339Nano), "actor": map[string]string{"type": "system", "id": "plan-worker"}, "action": action, "subject": map[string]string{"type": "request", "id": id}, "requestId": id, "outcome": outcome, "metadata": metadata}
	var events []json.RawMessage
	if len(r["auditEvents"]) > 0 {
		if err := json.Unmarshal(r["auditEvents"], &events); err != nil {
			return err
		}
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	events = append(events, encoded)
	set(r, "auditEvents", events)
	return nil
}
