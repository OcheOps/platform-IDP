package githubapp

import (
	"context"
	"fmt"
	"net/http"

	"github.com/OcheOps/platform-IDP/internal/applyworker"
)

func (client *Client) ObserveApply(ctx context.Context, work applyworker.Work) (applyworker.Observation, error) {
	var pull struct {
		State          string `json:"state"`
		Merged         bool   `json:"merged"`
		MergeCommitSHA string `json:"merge_commit_sha"`
		Head           struct {
			SHA string `json:"sha"`
		} `json:"head"`
	}
	repository := applyRepoPath(work)
	if _, err := client.request(ctx, http.MethodGet, fmt.Sprintf("%s/pulls/%d", repository, work.PullRequestNumber), nil, &pull, http.StatusOK); err != nil {
		return applyworker.Observation{}, err
	}
	if pull.State == "closed" && !pull.Merged {
		return applyworker.Observation{State: applyworker.Cancelled}, nil
	}
	if pull.Head.SHA != work.HeadSHA {
		return applyworker.Observation{State: applyworker.Invalidated, HeadSHA: pull.Head.SHA, FailureCode: "apply_head_changed"}, nil
	}
	if !pull.Merged || pull.State != "closed" || pull.MergeCommitSHA == "" {
		return applyworker.Observation{State: applyworker.Pending}, nil
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
	if _, err := client.request(ctx, http.MethodGet, repository+"/commits/"+escape(pull.MergeCommitSHA)+"/check-runs", nil, &checks, http.StatusOK); err != nil {
		return applyworker.Observation{}, err
	}
	var latestID int64
	state, failure := applyworker.Pending, ""
	for _, check := range checks.CheckRuns {
		if check.Name != "apply" || check.App.Slug != "github-actions" || check.ID <= latestID {
			continue
		}
		latestID = check.ID
		if check.HeadSHA != pull.MergeCommitSHA {
			state, failure = applyworker.Failed, "apply_check_head_mismatch"
		} else if check.Status != "completed" {
			state, failure = applyworker.Applying, ""
		} else if check.Conclusion != "success" {
			state, failure = applyworker.Failed, "apply_checks_failed"
		} else {
			state, failure = applyworker.Succeeded, ""
		}
	}
	if state == applyworker.Succeeded {
		var statuses []struct{ State, Context, SHA string }
		if _, err := client.request(ctx, http.MethodGet, repository+"/commits/"+escape(pull.MergeCommitSHA)+"/statuses", nil, &statuses, http.StatusOK); err != nil {
			return applyworker.Observation{}, err
		}
		want := "platform/apply/" + work.PlanDigest
		verified := false
		for _, status := range statuses {
			if status.Context == want && status.State == "success" && status.SHA == pull.MergeCommitSHA {
				verified = true
				break
			}
		}
		if !verified {
			return applyworker.Observation{State: applyworker.Failed, MergeSHA: pull.MergeCommitSHA, FailureCode: "apply_attestation_missing"}, nil
		}
	}
	return applyworker.Observation{State: state, MergeSHA: pull.MergeCommitSHA, FailureCode: failure}, nil
}

func applyRepoPath(work applyworker.Work) string {
	return "/repos/" + escape(work.Organization) + "/" + escape(work.Repository)
}
