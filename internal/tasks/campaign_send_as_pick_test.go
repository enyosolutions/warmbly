package tasks

import (
	"testing"

	"github.com/warmbly/warmbly/internal/repository"
)

func TestPickLeadSendAs(t *testing.T) {
	first := func(int) int { return 0 }
	last := func(n int) int { return n - 1 }

	cases := []struct {
		name      string
		in        repository.CampaignLeadSendAs
		intn      func(int) int
		wantAlias string
		wantHeld  bool
	}{
		{"follow-up keeps its alias", repository.CampaignLeadSendAs{BoundHere: true, LeadAlias: "b@acme.test", LeadAliasVerified: true, Configured: 2, Verified: []string{"a@acme.test"}}, last, "b@acme.test", false},
		{"follow-up keeps the mailbox address it started on", repository.CampaignLeadSendAs{BoundHere: true, LeadAlias: "", LeadAliasVerified: true, Configured: 2, Verified: []string{"a@acme.test"}}, last, "", false},
		{"follow-up on a revoked alias waits", repository.CampaignLeadSendAs{BoundHere: true, LeadAlias: "b@acme.test", LeadAliasVerified: false, Configured: 1, Verified: []string{"a@acme.test"}}, last, "b@acme.test", true},
		{"new lead with no campaign alias uses the mailbox", repository.CampaignLeadSendAs{}, last, "", false},
		{"new lead draws from the verified aliases", repository.CampaignLeadSendAs{Configured: 3, Verified: []string{"a@acme.test", "c@acme.test"}}, last, "c@acme.test", false},
		{"new lead draws the first", repository.CampaignLeadSendAs{Configured: 3, Verified: []string{"a@acme.test", "c@acme.test"}}, first, "a@acme.test", false},
		{"new lead with every alias revoked is held", repository.CampaignLeadSendAs{Configured: 2}, last, "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			alias, held := pickLeadSendAs(c.in, c.intn)
			if alias != c.wantAlias || held != c.wantHeld {
				t.Fatalf("pickLeadSendAs = %q, %v; want %q, %v", alias, held, c.wantAlias, c.wantHeld)
			}
		})
	}
}
