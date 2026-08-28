package approvalworker_test

import (
	"context"
	"github.com/OcheOps/platform-IDP/internal/approvalworker"
	"testing"
)

func TestIndependentApprovalsArePublishedBeforeStateChange(t *testing.T) {
	s := &store{work: approvalworker.Work{RequestID: "request-1"}}
	g := &provider{e: approvalworker.Evidence{EngineeringApprover: "eng", PlatformApprover: "platform"}}
	if err := approvalworker.New(s, g).Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !g.published || !s.approved {
		t.Fatal("approval was not published and persisted")
	}
}

type store struct {
	work     approvalworker.Work
	approved bool
}

func (s *store) Next(context.Context) (approvalworker.Work, error) { return s.work, nil }
func (s *store) Approved(context.Context, string, approvalworker.Evidence) error {
	s.approved = true
	return nil
}

type provider struct {
	e         approvalworker.Evidence
	published bool
}

func (p *provider) ObserveApprovals(context.Context, approvalworker.Work) (approvalworker.Evidence, bool, error) {
	return p.e, true, nil
}
func (p *provider) PublishApproval(context.Context, approvalworker.Work, approvalworker.Evidence) error {
	p.published = true
	return nil
}
