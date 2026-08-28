package githubapp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/OcheOps/platform-IDP/internal/configworker"
)

const configRepositoryDescription = "Platform IDP desired state (schema v1)"
const configRulesetName = "platform-config-protection"

var configServicePattern = regexp.MustCompile(`^[a-z](?:[a-z0-9-]{0,38}[a-z0-9])?$`)
var requestIDPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func (client *Client) EnsureChange(ctx context.Context, work configworker.Work) (string, error) {
	if !configServicePattern.MatchString(work.ServiceName) || !requestIDPattern.MatchString(work.RequestID) || work.Branch != "platform/request-"+work.RequestID || strings.TrimSpace(work.Organization) == "" || strings.TrimSpace(work.Repository) == "" {
		return "", errors.New("platform-config work identity is invalid")
	}
	if err := client.ensureConfigRepository(ctx, work); err != nil {
		return "", err
	}
	if err := client.ensureConfigPolicy(ctx, work); err != nil {
		return "", err
	}
	var branch gitRef
	status, err := client.request(ctx, http.MethodGet, configRepoPath(work)+"/git/ref/heads/"+escape(work.Branch), nil, &branch, http.StatusOK, http.StatusNotFound)
	if err != nil {
		return "", err
	}
	files, err := configFiles(work)
	if err != nil {
		return "", err
	}
	if status == http.StatusOK {
		if err := client.verifyConfigFiles(ctx, work, files); err != nil {
			return "", err
		}
		return branch.Object.SHA, nil
	}
	var main gitRef
	if _, err := client.request(ctx, http.MethodGet, configRepoPath(work)+"/git/ref/heads/main", nil, &main, http.StatusOK); err != nil {
		return "", err
	}
	var base struct {
		Tree struct {
			SHA string `json:"sha"`
		} `json:"tree"`
	}
	if _, err := client.request(ctx, http.MethodGet, configRepoPath(work)+"/git/commits/"+escape(main.Object.SHA), nil, &base, http.StatusOK); err != nil {
		return "", err
	}
	entries := make([]map[string]string, 0, len(files))
	for path, content := range files {
		entries = append(entries, map[string]string{"path": path, "mode": "100644", "type": "blob", "content": string(content)})
	}
	var tree struct {
		SHA string `json:"sha"`
	}
	if _, err := client.request(ctx, http.MethodPost, configRepoPath(work)+"/git/trees", map[string]any{"base_tree": base.Tree.SHA, "tree": entries}, &tree, http.StatusCreated); err != nil {
		return "", err
	}
	var commit struct {
		SHA string `json:"sha"`
	}
	message := "Request staging service " + work.ServiceName
	if _, err := client.request(ctx, http.MethodPost, configRepoPath(work)+"/git/commits", map[string]any{"message": message, "tree": tree.SHA, "parents": []string{main.Object.SHA}}, &commit, http.StatusCreated); err != nil {
		return "", err
	}
	if _, err := client.request(ctx, http.MethodPost, configRepoPath(work)+"/git/refs", map[string]string{"ref": "refs/heads/" + work.Branch, "sha": commit.SHA}, nil, http.StatusCreated); err != nil {
		return "", err
	}
	return commit.SHA, nil
}

func (client *Client) EnsurePullRequest(ctx context.Context, work configworker.Work, headSHA string) (int64, error) {
	if work.PullRequestNumber > 0 {
		if err := client.VerifyPullRequest(ctx, work, work.PullRequestNumber, headSHA); err != nil {
			return 0, err
		}
		return work.PullRequestNumber, nil
	}
	path := configRepoPath(work) + "/pulls?state=open&base=main&head=" + url.QueryEscape(work.Organization+":"+work.Branch)
	var existing []pullRequest
	if _, err := client.request(ctx, http.MethodGet, path, nil, &existing, http.StatusOK); err != nil {
		return 0, err
	}
	if len(existing) > 1 {
		return 0, errors.New("multiple open platform-config pull requests match the request branch")
	}
	if len(existing) == 1 {
		if err := verifyConfigPull(existing[0], work, headSHA); err != nil {
			return 0, err
		}
		return existing[0].Number, nil
	}
	var estimate struct {
		Digest             string `json:"digest"`
		IncrementalService struct {
			Display string `json:"display"`
		} `json:"incrementalService"`
		PreviewScenario struct {
			Display string `json:"display"`
		} `json:"previewScenario"`
	}
	if err := json.Unmarshal(work.Estimate, &estimate); err != nil {
		return 0, fmt.Errorf("decode cost estimate: %w", err)
	}
	body := fmt.Sprintf("Platform request `%s`.\n\nEstimate digest: `%s`\nIncremental monthly: %s\nOne-preview scenario: %s\n\nThe estimate is advisory; review its committed assumptions before approval.", work.RequestID, estimate.Digest, estimate.IncrementalService.Display, estimate.PreviewScenario.Display)
	var created pullRequest
	if _, err := client.request(ctx, http.MethodPost, configRepoPath(work)+"/pulls", map[string]any{"title": "Provision " + work.ServiceName + " in staging", "head": work.Branch, "base": "main", "body": body, "maintainer_can_modify": false}, &created, http.StatusCreated); err != nil {
		return 0, err
	}
	if err := verifyConfigPull(created, work, headSHA); err != nil {
		return 0, err
	}
	return created.Number, nil
}

func (client *Client) VerifyPullRequest(ctx context.Context, work configworker.Work, number int64, headSHA string) error {
	var found pullRequest
	if _, err := client.request(ctx, http.MethodGet, fmt.Sprintf("%s/pulls/%d", configRepoPath(work), number), nil, &found, http.StatusOK); err != nil {
		return err
	}
	if err := verifyConfigPull(found, work, headSHA); err != nil {
		return err
	}
	var files []struct{ Filename, Status string }
	if _, err := client.request(ctx, http.MethodGet, fmt.Sprintf("%s/pulls/%d/files?per_page=100", configRepoPath(work), number), nil, &files, http.StatusOK); err != nil {
		return err
	}
	expected, err := configFiles(work)
	if err != nil {
		return err
	}
	if len(files) != len(expected) {
		return errors.New("platform-config pull request changes unexpected files")
	}
	for _, file := range files {
		if _, ok := expected[file.Filename]; !ok || file.Status != "added" {
			return fmt.Errorf("platform-config pull request has unexpected %s change to %s", file.Status, file.Filename)
		}
	}
	return nil
}

type gitRef struct {
	Object struct {
		SHA string `json:"sha"`
	} `json:"object"`
}
type pullRequest struct {
	Number int64  `json:"number"`
	State  string `json:"state"`
	Title  string `json:"title"`
	Body   string `json:"body"`
	Head   struct {
		Ref string `json:"ref"`
		SHA string `json:"sha"`
	} `json:"head"`
	Base struct {
		Ref string `json:"ref"`
	} `json:"base"`
}

func (client *Client) ensureConfigRepository(ctx context.Context, work configworker.Work) error {
	var found repository
	status, err := client.request(ctx, http.MethodGet, configRepoPath(work), nil, &found, http.StatusOK, http.StatusNotFound)
	if err != nil {
		return err
	}
	if status == http.StatusNotFound {
		if _, err := client.request(ctx, http.MethodPost, "/orgs/"+escape(work.Organization)+"/repos", map[string]any{"name": work.Repository, "description": configRepositoryDescription, "private": true, "auto_init": false, "has_issues": true, "has_projects": false, "has_wiki": false}, &found, http.StatusCreated); err != nil {
			return err
		}
		if err := client.initializeConfigRepository(ctx, work); err != nil {
			return err
		}
	}
	if found.ID <= 0 || !found.Private || !strings.EqualFold(found.FullName, work.Organization+"/"+work.Repository) || found.Description != configRepositoryDescription {
		return errors.New("platform-config repository identity does not match platform policy")
	}
	return nil
}
func (client *Client) verifyConfigFiles(ctx context.Context, work configworker.Work, expected map[string][]byte) error {
	for path, want := range expected {
		var found struct {
			Content  string `json:"content"`
			Encoding string `json:"encoding"`
		}
		endpoint := configRepoPath(work) + "/contents/" + escape(path) + "?ref=" + url.QueryEscape(work.Branch)
		if _, err := client.request(ctx, http.MethodGet, endpoint, nil, &found, http.StatusOK); err != nil {
			return err
		}
		if found.Encoding != "base64" {
			return errors.New("platform-config content is not base64 encoded")
		}
		got, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(found.Content, "\n", ""))
		if err != nil {
			return err
		}
		if string(got) != string(want) {
			return fmt.Errorf("platform-config branch contains divergent %s", path)
		}
	}
	return nil
}
func configFiles(work configworker.Work) (map[string][]byte, error) {
	var desired struct {
		Metadata struct{ Name, RequestID string } `json:"metadata"`
	}
	if err := json.Unmarshal(work.Request, &desired); err != nil {
		return nil, fmt.Errorf("decode request identity: %w", err)
	}
	if desired.Metadata.Name != work.ServiceName || desired.Metadata.RequestID != work.RequestID {
		return nil, errors.New("service request identity does not match config work")
	}
	request, err := prettyJSON(work.Request)
	if err != nil {
		return nil, fmt.Errorf("normalize request: %w", err)
	}
	estimate, err := prettyJSON(work.Estimate)
	if err != nil {
		return nil, fmt.Errorf("normalize estimate: %w", err)
	}
	prefix := "services/" + work.ServiceName + "/staging/"
	return map[string][]byte{prefix + "request.json": request, prefix + "estimate.json": estimate}, nil
}
func prettyJSON(data []byte) ([]byte, error) {
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		return nil, err
	}
	result, err := json.MarshalIndent(value, "", "  ")
	return append(result, '\n'), err
}
func verifyConfigPull(found pullRequest, work configworker.Work, headSHA string) error {
	var estimate struct {
		Digest string `json:"digest"`
	}
	if json.Unmarshal(work.Estimate, &estimate) != nil || estimate.Digest == "" {
		return errors.New("cost estimate digest is required")
	}
	if found.Number <= 0 || found.State != "open" || found.Title != "Provision "+work.ServiceName+" in staging" || found.Head.Ref != work.Branch || found.Head.SHA != headSHA || found.Base.Ref != "main" || !strings.Contains(found.Body, work.RequestID) || !strings.Contains(found.Body, estimate.Digest) {
		return errors.New("platform-config pull request identity does not match the request")
	}
	return nil
}
func configRepoPath(work configworker.Work) string {
	return "/repos/" + escape(work.Organization) + "/" + escape(work.Repository)
}

func (client *Client) ensureConfigPolicy(ctx context.Context, work configworker.Work) error {
	if _, err := client.request(ctx, http.MethodPatch, configRepoPath(work), map[string]any{"private": true, "default_branch": "main", "delete_branch_on_merge": true, "allow_squash_merge": true, "allow_merge_commit": false, "allow_rebase_merge": false}, nil, http.StatusOK); err != nil {
		return err
	}
	var existing []struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
	}
	if _, err := client.request(ctx, http.MethodGet, configRepoPath(work)+"/rulesets", nil, &existing, http.StatusOK); err != nil {
		return err
	}
	for _, item := range existing {
		if item.Name != configRulesetName {
			continue
		}
		var detail ruleset
		if _, err := client.request(ctx, http.MethodGet, fmt.Sprintf("%s/rulesets/%d", configRepoPath(work), item.ID), nil, &detail, http.StatusOK); err != nil {
			return err
		}
		if configRulesetMatches(detail) {
			return nil
		}
		_, err := client.request(ctx, http.MethodPut, fmt.Sprintf("%s/rulesets/%d", configRepoPath(work), item.ID), configRuleset(), nil, http.StatusOK)
		return err
	}
	_, err := client.request(ctx, http.MethodPost, configRepoPath(work)+"/rulesets", configRuleset(), nil, http.StatusCreated)
	return err
}
func configRuleset() map[string]any {
	return map[string]any{
		"name": configRulesetName, "target": "branch", "enforcement": "active",
		"conditions": map[string]any{"ref_name": map[string]any{"include": []string{"refs/heads/main"}, "exclude": []string{}}},
		"rules": []map[string]any{
			{"type": "deletion"},
			{"type": "non_fast_forward"},
			{"type": "required_linear_history"},
			{"type": "required_status_checks", "parameters": map[string]any{"strict_required_status_checks_policy": true, "required_status_checks": []map[string]string{{"context": "build"}, {"context": "platform/approvals"}}}},
			{"type": "pull_request", "parameters": map[string]any{"required_approving_review_count": 2, "dismiss_stale_reviews_on_push": true, "require_code_owner_review": false, "require_last_push_approval": true, "required_review_thread_resolution": true}},
		},
	}
}
func configRulesetMatches(value ruleset) bool {
	if value.Name != configRulesetName || value.Target != "branch" || value.Enforcement != "active" || len(value.BypassActors) != 0 || len(value.Conditions.RefName.Include) != 1 || value.Conditions.RefName.Include[0] != "refs/heads/main" || len(value.Conditions.RefName.Exclude) != 0 {
		return false
	}
	types := map[string]bool{}
	var checks, pull map[string]any
	for _, rule := range value.Rules {
		types[rule.Type] = true
		if rule.Type == "required_status_checks" {
			checks = rule.Parameters
		}
		if rule.Type == "pull_request" {
			pull = rule.Parameters
		}
	}
	required, ok := checks["required_status_checks"].([]any)
	contexts := map[string]bool{}
	if ok {
		for _, raw := range required {
			if item, ok := raw.(map[string]any); ok {
				context, _ := item["context"].(string)
				contexts[context] = true
			}
		}
	}
	return len(value.Rules) == 5 && types["deletion"] && types["non_fast_forward"] && types["required_linear_history"] && types["required_status_checks"] && types["pull_request"] && len(contexts) == 2 && contexts["build"] && contexts["platform/approvals"] && truth(checks["strict_required_status_checks_policy"]) && number(pull["required_approving_review_count"]) == 2 && truth(pull["dismiss_stale_reviews_on_push"]) && !truth(pull["require_code_owner_review"]) && truth(pull["require_last_push_approval"]) && truth(pull["required_review_thread_resolution"])
}
