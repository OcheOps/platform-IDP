package approvalworker

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

type FileStore struct{ root, organization, repository, engineeringTeam, platformTeam string }

func NewFileStore(root, organization, repository, engineeringTeam, platformTeam string) *FileStore {
	return &FileStore{root, organization, repository, engineeringTeam, platformTeam}
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
		r, err := read(filepath.Join(s.root, "requests", entry.Name()))
		if err != nil {
			return Work{}, err
		}
		if text(r["requestState"]) != "awaiting_approval" {
			continue
		}
		work := Work{RequestID: text(r["requestId"]), Requester: text(r["actor"]), Organization: s.organization, Repository: s.repository, HeadSHA: text(r["configHeadSHA"]), PlanDigest: text(r["planDigest"]), PullRequestNumber: number(r["configPullRequestNumber"]), EngineeringOwnerTeam: s.engineeringTeam, PlatformOwnerTeam: s.platformTeam}
		if work.RequestID == "" || work.Requester == "" || work.HeadSHA == "" || work.PlanDigest == "" || work.PullRequestNumber <= 0 || work.EngineeringOwnerTeam == work.PlatformOwnerTeam {
			return Work{}, errors.New("approval work record is incomplete")
		}
		return work, nil
	}
	return Work{}, ErrNoWork
}
func (s *FileStore) Approved(_ context.Context, id string, e Evidence) error {
	return s.update(id, func(r map[string]json.RawMessage) error {
		set(r, "requestState", "approved")
		set(r, "engineeringApprover", e.EngineeringApprover)
		set(r, "platformApprover", e.PlatformApprover)
		set(r, "failureCode", "")
		for role, user := range map[string]string{"engineering_owner": e.EngineeringApprover, "platform_owner": e.PlatformApprover} {
			if err := event(r, id, role, user, text(r["configHeadSHA"]), text(r["planDigest"])); err != nil {
				return err
			}
		}
		return nil
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
	f, err := os.CreateTemp(filepath.Dir(path), ".approval-worker-*.tmp")
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
func event(r map[string]json.RawMessage, id, role, user, head, digest string) error {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return err
	}
	raw[6] = raw[6]&15 | 64
	raw[8] = raw[8]&63 | 128
	eid := fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", raw[0:4], raw[4:6], raw[6:8], raw[8:10], raw[10:16])
	value := map[string]any{"id": eid, "occurredAt": time.Now().UTC().Format(time.RFC3339Nano), "actor": map[string]string{"type": "github", "id": user}, "action": "approval.granted", "subject": map[string]string{"type": "approval", "id": id + ":" + role}, "requestId": id, "outcome": "succeeded", "metadata": map[string]any{"role": role, "headSHA": head, "planDigest": digest}}
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
