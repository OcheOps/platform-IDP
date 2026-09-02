package githubapp

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/OcheOps/platform-IDP/internal/deployworker"
)

var imageStatusPattern = regexp.MustCompile(`^platform/image/(sha256:[a-f0-9]{64})$`)

func (client *Client) ObserveImage(ctx context.Context, work deployworker.Work) (deployworker.Observation, error) {
	var statuses []struct{ State, Context, SHA string }
	path := "/repos/" + escape(work.Organization) + "/" + escape(work.ServiceName) + "/commits/main/statuses?per_page=100"
	if _, err := client.request(ctx, http.MethodGet, path, nil, &statuses, http.StatusOK); err != nil {
		return deployworker.Observation{}, err
	}
	var found string
	for _, status := range statuses {
		match := imageStatusPattern.FindStringSubmatch(status.Context)
		if len(match) != 2 || status.State != "success" || status.SHA == "" {
			continue
		}
		if found != "" && found != match[1] {
			return deployworker.Observation{}, errors.New("multiple image digests are published for the application")
		}
		found = match[1]
	}
	if found == "" {
		return deployworker.Observation{ImageReady: false}, nil
	}
	return deployworker.Observation{ImageReady: true, ImageRepository: work.AWSAccountID + ".dkr.ecr." + work.AWSRegion + ".amazonaws.com/platform/" + work.ServiceName, ImageDigest: found}, nil
}

func (client *Client) EnsureDeploymentChange(ctx context.Context, work deployworker.Work, values []byte) (int64, error) {
	repository := deploymentRepoPath(work)
	branch := "platform/deploy-" + work.RequestID
	path := "services/" + work.ServiceName + "/staging/values.json"
	var ref struct {
		Object struct {
			SHA string `json:"sha"`
		} `json:"object"`
	}
	status, err := client.request(ctx, http.MethodGet, repository+"/git/ref/heads/"+escape(branch), nil, &ref, http.StatusOK, http.StatusNotFound)
	if err != nil {
		return 0, err
	}
	if status == http.StatusOK {
		if err := client.verifyDeploymentContent(ctx, work, ref.Object.SHA, values); err != nil {
			return 0, err
		}
	} else {
		var main gitRef
		if _, err := client.request(ctx, http.MethodGet, repository+"/git/ref/heads/main", nil, &main, http.StatusOK); err != nil {
			return 0, err
		}
		var tree struct {
			SHA string `json:"sha"`
		}
		if _, err := client.request(ctx, http.MethodPost, repository+"/git/trees", map[string]any{"base_tree": main.Object.SHA, "tree": []map[string]string{{"path": path, "mode": "100644", "type": "blob", "content": string(values)}}}, &tree, http.StatusCreated); err != nil {
			return 0, err
		}
		var commit struct {
			SHA string `json:"sha"`
		}
		if _, err := client.request(ctx, http.MethodPost, repository+"/git/commits", map[string]any{"message": "Deploy " + work.ServiceName + " to staging", "tree": tree.SHA, "parents": []string{main.Object.SHA}}, &commit, http.StatusCreated); err != nil {
			return 0, err
		}
		if _, err := client.request(ctx, http.MethodPost, repository+"/git/refs", map[string]string{"ref": "refs/heads/" + branch, "sha": commit.SHA}, nil, http.StatusCreated); err != nil {
			return 0, err
		}
	}
	query := repository + "/pulls?state=open&base=main&head=" + url.QueryEscape(work.Organization+":"+branch)
	var pulls []pullRequest
	if _, err := client.request(ctx, http.MethodGet, query, nil, &pulls, http.StatusOK); err != nil {
		return 0, err
	}
	if len(pulls) > 1 {
		return 0, errors.New("multiple open deployment pull requests match the request")
	}
	if len(pulls) == 1 {
		if err := verifyDeploymentPull(pulls[0], work, values); err != nil {
			return 0, err
		}
		return pulls[0].Number, nil
	}
	var created pullRequest
	body := fmt.Sprintf("Platform deployment for request `%s`. Image digest: `%s`", work.RequestID, work.ImageDigest)
	if _, err := client.request(ctx, http.MethodPost, repository+"/pulls", map[string]any{"title": "Deploy " + work.ServiceName + " in staging", "head": branch, "base": "main", "body": body, "maintainer_can_modify": false}, &created, http.StatusCreated); err != nil {
		return 0, err
	}
	if err := verifyDeploymentPull(created, work, values); err != nil {
		return 0, err
	}
	return created.Number, nil
}

func (client *Client) VerifyDeploymentChange(ctx context.Context, work deployworker.Work, number int64, values []byte) error {
	var found pullRequest
	if _, err := client.request(ctx, http.MethodGet, fmt.Sprintf("%s/pulls/%d", deploymentRepoPath(work), number), nil, &found, http.StatusOK); err != nil {
		return err
	}
	if err := verifyDeploymentPull(found, work, values); err != nil {
		return err
	}
	return client.verifyDeploymentContent(ctx, work, found.Head.SHA, values)
}

func (client *Client) verifyDeploymentContent(ctx context.Context, work deployworker.Work, _ string, values []byte) error {
	var found struct {
		Content  string `json:"content"`
		Encoding string `json:"encoding"`
	}
	path := deploymentRepoPath(work) + "/contents/services/" + escape(work.ServiceName) + "/staging/values.json?ref=" + url.QueryEscape("platform/deploy-"+work.RequestID)
	if _, err := client.request(ctx, http.MethodGet, path, nil, &found, http.StatusOK); err != nil {
		return err
	}
	if found.Encoding != "base64" {
		return errors.New("GitOps values are not base64 encoded")
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(found.Content, "\n", ""))
	if err != nil {
		return err
	}
	if string(decoded) != string(values) {
		return errors.New("deployment branch contains divergent GitOps values")
	}
	return nil
}

func verifyDeploymentPull(found pullRequest, work deployworker.Work, values []byte) error {
	if found.Number <= 0 || found.State != "open" || found.Title != "Deploy "+work.ServiceName+" in staging" || found.Head.Ref != "platform/deploy-"+work.RequestID || found.Base.Ref != "main" || !strings.Contains(found.Body, work.RequestID) || !strings.Contains(found.Body, work.ImageDigest) {
		return errors.New("deployment pull request identity does not match the request")
	}
	_ = values
	return nil
}

func deploymentRepoPath(work deployworker.Work) string {
	return "/repos/" + escape(work.Organization) + "/" + escape(work.Repository)
}
