package githubapp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"

	"github.com/OcheOps/platform-IDP/internal/planworker"
)

var planStatusPattern = regexp.MustCompile(`^platform/plan/(sha256:[0-9a-f]{64})$`)
var digestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

func (client *Client) ObservePlan(ctx context.Context, work planworker.Work) (planworker.Observation, error) {
	var pull pullRequest
	if _, err := client.request(ctx, http.MethodGet, fmt.Sprintf("%s/pulls/%d", planRepoPath(work), work.PullRequestNumber), nil, &pull, http.StatusOK); err != nil {
		return planworker.Observation{}, err
	}
	if pull.State != "open" || pull.Head.SHA != work.HeadSHA {
		return planworker.Observation{State: planworker.Failed, FailureCode: "plan_head_changed"}, nil
	}
	var checks struct {
		CheckRuns []struct {
			ID         int64  `json:"id"`
			Name       string `json:"name"`
			Status     string `json:"status"`
			Conclusion string `json:"conclusion"`
			HeadSHA    string `json:"head_sha"`
			App        struct {
				Slug string `json:"slug"`
			} `json:"app"`
		} `json:"check_runs"`
	}
	if _, err := client.request(ctx, http.MethodGet, planRepoPath(work)+"/commits/"+escape(work.HeadSHA)+"/check-runs", nil, &checks, http.StatusOK); err != nil {
		return planworker.Observation{}, err
	}
	var latest *struct {
		ID         int64  `json:"id"`
		Name       string `json:"name"`
		Status     string `json:"status"`
		Conclusion string `json:"conclusion"`
		HeadSHA    string `json:"head_sha"`
		App        struct {
			Slug string `json:"slug"`
		} `json:"app"`
	}
	for _, check := range checks.CheckRuns {
		if check.Name != "build" || check.App.Slug != "github-actions" {
			continue
		}
		if latest == nil || check.ID > latest.ID {
			copy := check
			latest = &copy
		}
	}
	if latest == nil {
		return planworker.Observation{State: planworker.Pending}, nil
	}
	if latest.HeadSHA != work.HeadSHA {
		return planworker.Observation{State: planworker.Failed, FailureCode: "plan_check_head_mismatch"}, nil
	}
	if latest.Status != "completed" {
		return planworker.Observation{State: planworker.Pending}, nil
	}
	if latest.Conclusion != "success" {
		return planworker.Observation{State: planworker.Failed, FailureCode: "plan_checks_failed"}, nil
	}
	var statuses []struct {
		State   string `json:"state"`
		Context string `json:"context"`
		SHA     string `json:"sha"`
	}
	if _, err := client.request(ctx, http.MethodGet, planRepoPath(work)+"/commits/"+escape(work.HeadSHA)+"/statuses", nil, &statuses, http.StatusOK); err != nil {
		return planworker.Observation{}, err
	}
	digest := ""
	for _, status := range statuses {
		match := planStatusPattern.FindStringSubmatch(status.Context)
		if len(match) != 2 {
			continue
		}
		if status.State != "success" || status.SHA != work.HeadSHA {
			return planworker.Observation{State: planworker.Failed, FailureCode: "plan_digest_status_invalid"}, nil
		}
		if digest != "" && digest != match[1] {
			return planworker.Observation{State: planworker.Failed, FailureCode: "plan_digest_ambiguous"}, nil
		}
		digest = match[1]
	}
	if digest == "" {
		return planworker.Observation{State: planworker.Pending}, nil
	}
	if !digestPattern.MatchString(work.EstimateDigest) {
		return planworker.Observation{}, errors.New("estimate digest is invalid")
	}
	return planworker.Observation{State: planworker.Succeeded, PlanDigest: digest}, nil
}
func planRepoPath(work planworker.Work) string {
	return "/repos/" + escape(work.Organization) + "/" + escape(work.Repository)
}
