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
	"github.com/OcheOps/platform-IDP/internal/repositoryworker"
)

const configRepositoryDescription = "Platform IDP desired state"

var configServicePattern = regexp.MustCompile(`^[a-z](?:[a-z0-9-]{0,38}[a-z0-9])?$`)
var requestIDPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func (client *Client) EnsureChange(ctx context.Context, work configworker.Work) (string, error) {
	if !configServicePattern.MatchString(work.ServiceName) || !requestIDPattern.MatchString(work.RequestID) || work.Branch != "platform/request-"+work.RequestID || strings.TrimSpace(work.Organization) == "" || strings.TrimSpace(work.Repository) == "" {
		return "", errors.New("platform-config work identity is invalid")
	}
	created, err := client.ensureConfigRepository(ctx, work)
	if err != nil {
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
		if created {
			return "", repositoryworker.Transient(err)
		}
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

func (client *Client) ensureConfigRepository(ctx context.Context, work configworker.Work) (bool, error) {
	var found repository
	status, err := client.request(ctx, http.MethodGet, configRepoPath(work), nil, &found, http.StatusOK, http.StatusNotFound)
	if err != nil {
		return false, err
	}
	created := false
	if status == http.StatusNotFound {
		if _, err := client.request(ctx, http.MethodPost, "/orgs/"+escape(work.Organization)+"/repos", map[string]any{"name": work.Repository, "description": configRepositoryDescription, "private": true, "auto_init": true, "has_issues": true, "has_projects": false, "has_wiki": false}, &found, http.StatusCreated); err != nil {
			return false, err
		}
		created = true
	}
	if found.ID <= 0 || !found.Private || !strings.EqualFold(found.FullName, work.Organization+"/"+work.Repository) || found.Description != configRepositoryDescription {
		return false, errors.New("platform-config repository identity does not match platform policy")
	}
	return created, nil
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
