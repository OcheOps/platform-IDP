package githubapp

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/OcheOps/platform-IDP/internal/repositoryworker"
)

const apiVersion = "2026-03-10"
const rulesetName = "platform-main-protection"
const initialCommitMessage = "Initialize service from platform golden path"

type TokenSource func(context.Context) (string, error)
type Client struct {
	baseURL string
	http    *http.Client
	token   TokenSource
}

func NewClient(baseURL string, httpClient *http.Client, token TokenSource) (*Client, error) {
	if err := validateBaseURL(baseURL); err != nil {
		return nil, fmt.Errorf("invalid GitHub API URL: %w", err)
	}
	if httpClient == nil || token == nil {
		return nil, errors.New("HTTP client and token source are required")
	}
	return &Client{strings.TrimRight(baseURL, "/"), httpClient, token}, nil
}

type repository struct {
	ID                  int64  `json:"id"`
	FullName            string `json:"full_name"`
	Private             bool   `json:"private"`
	DefaultBranch       string `json:"default_branch"`
	DeleteBranchOnMerge bool   `json:"delete_branch_on_merge"`
	Size                int    `json:"size"`
	Description         string `json:"description"`
	AllowSquashMerge    bool   `json:"allow_squash_merge"`
	AllowMergeCommit    bool   `json:"allow_merge_commit"`
	AllowRebaseMerge    bool   `json:"allow_rebase_merge"`
}
type ruleset struct {
	ID           int64             `json:"id"`
	Name         string            `json:"name"`
	Target       string            `json:"target"`
	Enforcement  string            `json:"enforcement"`
	BypassActors []json.RawMessage `json:"bypass_actors"`
	Conditions   struct {
		RefName struct{ Include, Exclude []string } `json:"ref_name"`
	} `json:"conditions"`
	Rules []struct {
		Type       string         `json:"type"`
		Parameters map[string]any `json:"parameters"`
	} `json:"rules"`
}

func (client *Client) EnsureRepository(ctx context.Context, work repositoryworker.Work) (int64, error) {
	var found repository
	if work.RepositoryID > 0 {
		if _, err := client.request(ctx, http.MethodGet, fmt.Sprintf("/repositories/%d", work.RepositoryID), nil, &found, http.StatusOK); err != nil {
			return 0, err
		}
		if err := verifyIdentity(found, work); err != nil {
			return 0, err
		}
		return found.ID, nil
	}
	status, err := client.request(ctx, http.MethodGet, repoPath(work), nil, &found, http.StatusOK, http.StatusNotFound)
	if err != nil {
		return 0, err
	}
	if status == http.StatusOK {
		if err := verifyIdentity(found, work); err != nil {
			return 0, err
		}
		return found.ID, nil
	}
	body := map[string]any{"name": work.Name, "description": ownershipMarker(work), "private": true, "has_issues": true, "has_projects": false, "has_wiki": false, "auto_init": false, "delete_branch_on_merge": true}
	if _, err := client.request(ctx, http.MethodPost, "/orgs/"+escape(work.Organization)+"/repos", body, &found, http.StatusCreated); err != nil {
		return 0, err
	}
	if err := verifyIdentity(found, work); err != nil {
		return 0, err
	}
	return found.ID, nil
}

func (client *Client) EnsureInitialCommit(ctx context.Context, work repositoryworker.Work, _ int64) error {
	status, err := client.request(ctx, http.MethodGet, repoPath(work)+"/git/ref/heads/main", nil, nil, http.StatusOK, http.StatusNotFound)
	if err != nil {
		return err
	}
	if status == http.StatusOK {
		return client.verifyTree(ctx, work)
	}
	entries, err := treeEntries(work.RenderedPath)
	if err != nil {
		return err
	}
	var tree struct {
		SHA string `json:"sha"`
	}
	if _, err := client.request(ctx, http.MethodPost, repoPath(work)+"/git/trees", map[string]any{"tree": entries}, &tree, http.StatusCreated); err != nil {
		return err
	}
	var commit struct {
		SHA string `json:"sha"`
	}
	if _, err := client.request(ctx, http.MethodPost, repoPath(work)+"/git/commits", map[string]any{"message": initialCommitMessage, "tree": tree.SHA, "parents": []string{}}, &commit, http.StatusCreated); err != nil {
		return err
	}
	_, err = client.request(ctx, http.MethodPost, repoPath(work)+"/git/refs", map[string]string{"ref": "refs/heads/main", "sha": commit.SHA}, nil, http.StatusCreated)
	return err
}

func (client *Client) EnsureSettings(ctx context.Context, work repositoryworker.Work, _ int64) error {
	if _, err := client.request(ctx, http.MethodPatch, repoPath(work), map[string]any{"description": ownershipMarker(work), "private": true, "default_branch": "main", "delete_branch_on_merge": true, "allow_squash_merge": true, "allow_merge_commit": false, "allow_rebase_merge": false}, nil, http.StatusOK); err != nil {
		return err
	}
	teamPath := "/orgs/" + escape(work.Organization) + "/teams/" + escape(work.OwnerTeam) + "/repos/" + escape(work.Organization) + "/" + escape(work.Name)
	if _, err := client.request(ctx, http.MethodPut, teamPath, map[string]string{"permission": "maintain"}, nil, http.StatusNoContent); err != nil {
		return err
	}
	var rulesets []struct {
		ID          int64  `json:"id"`
		Name        string `json:"name"`
		Enforcement string `json:"enforcement"`
	}
	if _, err := client.request(ctx, http.MethodGet, repoPath(work)+"/rulesets", nil, &rulesets, http.StatusOK); err != nil {
		return err
	}
	for _, rule := range rulesets {
		if rule.Name == rulesetName {
			var detail ruleset
			if _, err := client.request(ctx, http.MethodGet, fmt.Sprintf("%s/rulesets/%d", repoPath(work), rule.ID), nil, &detail, http.StatusOK); err != nil {
				return err
			}
			if rulesetMatches(detail) {
				return nil
			}
			_, err := client.request(ctx, http.MethodPut, fmt.Sprintf("%s/rulesets/%d", repoPath(work), rule.ID), mainRuleset(), nil, http.StatusOK)
			return err
		}
	}
	_, err := client.request(ctx, http.MethodPost, repoPath(work)+"/rulesets", mainRuleset(), nil, http.StatusCreated)
	return err
}

func (client *Client) Verify(ctx context.Context, work repositoryworker.Work, repositoryID int64) error {
	var found repository
	if _, err := client.request(ctx, http.MethodGet, fmt.Sprintf("/repositories/%d", repositoryID), nil, &found, http.StatusOK); err != nil {
		return err
	}
	if err := verifyIdentity(found, work); err != nil {
		return err
	}
	if found.DefaultBranch != "main" || !found.DeleteBranchOnMerge || !found.AllowSquashMerge || found.AllowMergeCommit || found.AllowRebaseMerge {
		return errors.New("repository settings do not match platform policy")
	}
	if err := client.verifyTree(ctx, work); err != nil {
		return err
	}
	var rulesets []struct {
		ID          int64  `json:"id"`
		Name        string `json:"name"`
		Enforcement string `json:"enforcement"`
	}
	if _, err := client.request(ctx, http.MethodGet, repoPath(work)+"/rulesets", nil, &rulesets, http.StatusOK); err != nil {
		return err
	}
	active := false
	for _, rule := range rulesets {
		if rule.Name == rulesetName {
			var detail ruleset
			if _, err := client.request(ctx, http.MethodGet, fmt.Sprintf("%s/rulesets/%d", repoPath(work), rule.ID), nil, &detail, http.StatusOK); err != nil {
				return err
			}
			active = rulesetMatches(detail)
		}
	}
	if !active {
		return errors.New("platform repository ruleset is not active")
	}
	var team struct {
		Permissions map[string]bool `json:"permissions"`
	}
	teamPath := "/orgs/" + escape(work.Organization) + "/teams/" + escape(work.OwnerTeam) + "/repos/" + escape(work.Organization) + "/" + escape(work.Name)
	if _, err := client.request(ctx, http.MethodGet, teamPath, nil, &team, http.StatusOK); err != nil {
		return err
	}
	if !team.Permissions["maintain"] && !team.Permissions["admin"] {
		return errors.New("owner team lacks maintain permission")
	}
	return nil
}

func (client *Client) request(ctx context.Context, method, path string, body, out any, allowed ...int) (int, error) {
	var payload io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return 0, err
		}
		payload = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, client.baseURL+path, payload)
	if err != nil {
		return 0, err
	}
	token, err := client.token(ctx)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-GitHub-Api-Version", apiVersion)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	response, err := client.http.Do(req)
	if err != nil {
		return 0, repositoryworker.Transient(err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return 0, repositoryworker.Transient(err)
	}
	for _, status := range allowed {
		if response.StatusCode == status {
			if out != nil && len(data) > 0 {
				if err := json.Unmarshal(data, out); err != nil {
					return 0, fmt.Errorf("decode GitHub response: %w", err)
				}
			}
			return status, nil
		}
	}
	apiErr := fmt.Errorf("GitHub API %s %s returned %d", method, path, response.StatusCode)
	if response.StatusCode == 429 || response.StatusCode >= 500 || (response.StatusCode == 403 && (response.Header.Get("Retry-After") != "" || response.Header.Get("X-RateLimit-Remaining") == "0")) {
		return response.StatusCode, repositoryworker.Transient(apiErr)
	}
	return response.StatusCode, apiErr
}

func treeEntries(root string) ([]map[string]string, error) {
	files, err := validatedFiles(root)
	if err != nil {
		return nil, err
	}
	var entries []map[string]string
	for _, file := range files {
		entries = append(entries, map[string]string{"path": file.path, "mode": file.mode, "type": "blob", "content": string(file.data)})
	}
	return entries, nil
}

type gitFile struct {
	path, mode, sha string
	data            []byte
}

func validatedFiles(root string) ([]gitFile, error) {
	var files []gitFile
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("repository contains symlink %s", path)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("repository contains non-regular file %s", path)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if bytes.IndexByte(data, 0) >= 0 {
			return fmt.Errorf("binary file is not supported: %s", path)
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		mode := "100644"
		if info.Mode()&0o111 != 0 {
			mode = "100755"
		}
		files = append(files, gitFile{filepath.ToSlash(relative), mode, gitObjectSHA("blob", data), data})
		return nil
	})
	return files, err
}
func mainRuleset() map[string]any {
	return map[string]any{
		"name": rulesetName, "target": "branch", "enforcement": "active",
		"conditions": map[string]any{"ref_name": map[string]any{"include": []string{"refs/heads/main"}, "exclude": []string{}}},
		"rules": []map[string]any{
			{"type": "deletion"}, {"type": "non_fast_forward"}, {"type": "required_linear_history"},
			{"type": "required_status_checks", "parameters": map[string]any{"strict_required_status_checks_policy": true, "required_status_checks": []map[string]string{{"context": "build"}}}},
			{"type": "pull_request", "parameters": map[string]any{"required_approving_review_count": 1, "dismiss_stale_reviews_on_push": true, "require_code_owner_review": true, "require_last_push_approval": true, "required_review_thread_resolution": true}},
		},
	}
}
func rulesetMatches(value ruleset) bool {
	if value.Name != rulesetName || value.Target != "branch" || value.Enforcement != "active" || len(value.BypassActors) != 0 || len(value.Conditions.RefName.Include) != 1 || value.Conditions.RefName.Include[0] != "refs/heads/main" || len(value.Conditions.RefName.Exclude) != 0 {
		return false
	}
	types := map[string]bool{}
	var pull map[string]any
	for _, rule := range value.Rules {
		types[rule.Type] = true
		if rule.Type == "pull_request" {
			pull = rule.Parameters
		}
	}
	if len(value.Rules) != 5 || !types["deletion"] || !types["non_fast_forward"] || !types["required_linear_history"] || !types["pull_request"] || !types["required_status_checks"] {
		return false
	}
	var checks map[string]any
	for _, rule := range value.Rules {
		if rule.Type == "required_status_checks" {
			checks = rule.Parameters
		}
	}
	required, ok := checks["required_status_checks"].([]any)
	checkOK := ok && len(required) == 1
	if checkOK {
		item, ok := required[0].(map[string]any)
		checkOK = ok && item["context"] == "build"
	}
	return checkOK && truth(checks["strict_required_status_checks_policy"]) && number(pull["required_approving_review_count"]) == 1 && truth(pull["dismiss_stale_reviews_on_push"]) && truth(pull["require_code_owner_review"]) && truth(pull["require_last_push_approval"]) && truth(pull["required_review_thread_resolution"])
}
func truth(value any) bool { result, _ := value.(bool); return result }
func number(value any) int { number, _ := value.(float64); return int(number) }

func (client *Client) verifyTree(ctx context.Context, work repositoryworker.Work) error {
	var ref struct {
		Object struct {
			SHA string `json:"sha"`
		} `json:"object"`
	}
	if _, err := client.request(ctx, http.MethodGet, repoPath(work)+"/git/ref/heads/main", nil, &ref, http.StatusOK); err != nil {
		return err
	}
	var commit struct {
		Tree struct {
			SHA string `json:"sha"`
		} `json:"tree"`
		Parents []struct {
			SHA string `json:"sha"`
		} `json:"parents"`
		Message string `json:"message"`
	}
	if _, err := client.request(ctx, http.MethodGet, repoPath(work)+"/git/commits/"+escape(ref.Object.SHA), nil, &commit, http.StatusOK); err != nil {
		return err
	}
	if len(commit.Parents) != 0 || commit.Message != initialCommitMessage {
		return errors.New("GitHub main is not the platform root initialization commit")
	}
	expected, err := expectedTreeSHA(work.RenderedPath)
	if err != nil {
		return err
	}
	if commit.Tree.SHA != expected {
		return errors.New("GitHub main tree does not match the rendered repository")
	}
	return nil
}

func expectedTreeSHA(root string) (string, error) {
	files, err := validatedFiles(root)
	if err != nil {
		return "", err
	}
	return treeSHA("", files), nil
}
func treeSHA(directory string, files []gitFile) string {
	type entry struct {
		name, mode, sha string
		directory       bool
	}
	entries := map[string]entry{}
	prefix := directory
	if prefix != "" {
		prefix += "/"
	}
	for _, file := range files {
		if !strings.HasPrefix(file.path, prefix) {
			continue
		}
		remainder := strings.TrimPrefix(file.path, prefix)
		parts := strings.SplitN(remainder, "/", 2)
		if len(parts) == 1 {
			entries[parts[0]] = entry{parts[0], file.mode, file.sha, false}
		} else if _, ok := entries[parts[0]]; !ok {
			child := prefix + parts[0]
			entries[parts[0]] = entry{parts[0], "40000", treeSHA(child, files), true}
		}
	}
	ordered := make([]entry, 0, len(entries))
	for _, item := range entries {
		ordered = append(ordered, item)
	}
	sort.Slice(ordered, func(i, j int) bool {
		left, right := ordered[i].name, ordered[j].name
		if ordered[i].directory {
			left += "/"
		}
		if ordered[j].directory {
			right += "/"
		}
		return left < right
	})
	var body bytes.Buffer
	for _, item := range ordered {
		fmt.Fprintf(&body, "%s %s%c", item.mode, item.name, byte(0))
		raw, _ := hex.DecodeString(item.sha)
		body.Write(raw)
	}
	return gitObjectSHA("tree", body.Bytes())
}
func gitObjectSHA(kind string, data []byte) string {
	hash := sha1.New()
	fmt.Fprintf(hash, "%s %d%c", kind, len(data), byte(0))
	_, _ = hash.Write(data)
	return hex.EncodeToString(hash.Sum(nil))
}
func verifyIdentity(found repository, work repositoryworker.Work) error {
	if found.ID <= 0 || !found.Private || !strings.EqualFold(found.FullName, work.Organization+"/"+work.Name) || found.Description != ownershipMarker(work) {
		return errors.New("GitHub repository identity does not match the request")
	}
	return nil
}
func ownershipMarker(work repositoryworker.Work) string {
	return "Managed by Platform IDP request " + work.RequestID
}
func repoPath(work repositoryworker.Work) string {
	return "/repos/" + escape(work.Organization) + "/" + escape(work.Name)
}
func escape(value string) string { return url.PathEscape(value) }
func validateBaseURL(value string) error {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" {
		return errors.New("invalid GitHub API URL")
	}
	if parsed.Scheme == "https" {
		return nil
	}
	host := parsed.Hostname()
	if parsed.Scheme == "http" && (host == "localhost" || host == "127.0.0.1" || host == "::1") {
		return nil
	}
	return errors.New("GitHub API URL must use HTTPS")
}
