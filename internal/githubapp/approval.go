package githubapp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/OcheOps/platform-IDP/internal/approvalworker"
)

func (client *Client) ObserveApprovals(ctx context.Context, work approvalworker.Work) (approvalworker.Evidence, bool, error) {
	var pull pullRequest
	if _, err := client.request(ctx, http.MethodGet, fmt.Sprintf("%s/pulls/%d", approvalRepoPath(work), work.PullRequestNumber), nil, &pull, http.StatusOK); err != nil {
		return approvalworker.Evidence{}, false, err
	}
	if pull.State != "open" || pull.Head.SHA != work.HeadSHA {
		return approvalworker.Evidence{}, false, errors.New("approval pull request head no longer matches reviewed plan")
	}
	var reviews []struct {
		ID       int64  `json:"id"`
		State    string `json:"state"`
		CommitID string `json:"commit_id"`
		User     struct {
			Login string `json:"login"`
		} `json:"user"`
	}
	if _, err := client.request(ctx, http.MethodGet, fmt.Sprintf("%s/pulls/%d/reviews?per_page=100", approvalRepoPath(work), work.PullRequestNumber), nil, &reviews, http.StatusOK); err != nil {
		return approvalworker.Evidence{}, false, err
	}
	type reviewState struct {
		ID              int64
		State, CommitID string
	}
	latest := map[string]reviewState{}
	for _, review := range reviews {
		login := strings.ToLower(review.User.Login)
		if login == "" || strings.EqualFold(login, work.Requester) {
			continue
		}
		if prior, ok := latest[login]; !ok || review.ID > prior.ID {
			latest[login] = reviewState{review.ID, review.State, review.CommitID}
		}
	}
	var engineering, platform []string
	for user, review := range latest {
		if review.State != "APPROVED" || review.CommitID != work.HeadSHA {
			continue
		}
		member, err := client.activeTeamMember(ctx, work.Organization, work.EngineeringOwnerTeam, user)
		if err != nil {
			return approvalworker.Evidence{}, false, err
		}
		if member {
			engineering = append(engineering, user)
		}
		member, err = client.activeTeamMember(ctx, work.Organization, work.PlatformOwnerTeam, user)
		if err != nil {
			return approvalworker.Evidence{}, false, err
		}
		if member {
			platform = append(platform, user)
		}
	}
	for _, engineer := range engineering {
		for _, owner := range platform {
			if engineer != owner {
				return approvalworker.Evidence{EngineeringApprover: engineer, PlatformApprover: owner}, true, nil
			}
		}
	}
	return approvalworker.Evidence{}, false, nil
}

func (client *Client) PublishApproval(ctx context.Context, work approvalworker.Work, _ approvalworker.Evidence) error {
	body := map[string]string{"state": "success", "context": "platform/approvals", "description": "Independent engineering and platform approvals verified"}
	_, err := client.request(ctx, http.MethodPost, approvalRepoPath(work)+"/statuses/"+escape(work.HeadSHA), body, nil, http.StatusCreated)
	return err
}
func (client *Client) activeTeamMember(ctx context.Context, organization, team, user string) (bool, error) {
	var membership struct {
		State string `json:"state"`
	}
	status, err := client.request(ctx, http.MethodGet, "/orgs/"+escape(organization)+"/teams/"+escape(team)+"/memberships/"+escape(user), nil, &membership, http.StatusOK, http.StatusNotFound)
	if err != nil {
		return false, err
	}
	return status == http.StatusOK && membership.State == "active", nil
}
func approvalRepoPath(work approvalworker.Work) string {
	return "/repos/" + escape(work.Organization) + "/" + escape(work.Repository)
}
