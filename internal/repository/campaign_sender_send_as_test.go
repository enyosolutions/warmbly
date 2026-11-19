package repository

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/models"
)

func TestApplyCampaignSendAs(t *testing.T) {
	mailbox := models.Email{ID: uuid.New(), Email: "jane@acme.test", SendAsEmail: "jane.alias@acme.test"}

	if got := ApplyCampaignSendAs(mailbox, nil, nil); got.SendAsEmail != mailbox.SendAsEmail || got.CampaignSendAsHeld {
		t.Fatalf("no campaign alias changed the mailbox: %+v", got)
	}
	// One alias still verified keeps the mailbox open to new leads, and the
	// pool names a usable alias rather than the revoked one.
	got := ApplyCampaignSendAs(mailbox, []string{"gone@acme.test", "sales@acme.test"}, []string{"GONE@acme.test"})
	if got.CampaignSendAsHeld || got.SendAsEmail != "sales@acme.test" {
		t.Fatalf("one verified alias left: from %q held %v, want sales@acme.test and open", got.SendAsEmail, got.CampaignSendAsHeld)
	}
	// None verified: held, and the alias kept, so nothing can fall back to
	// another address.
	got = ApplyCampaignSendAs(mailbox, []string{"gone@acme.test", "old@acme.test"}, []string{"gone@acme.test", "old@acme.test"})
	if !got.CampaignSendAsHeld || got.SendFrom() != "gone@acme.test" {
		t.Fatalf("no verified alias left: from %q held %v, want held on a campaign alias", got.SendFrom(), got.CampaignSendAsHeld)
	}
	if mailbox.SendAsEmail != "jane.alias@acme.test" || mailbox.CampaignSendAsHeld {
		t.Fatal("ApplyCampaignSendAs changed the caller's mailbox")
	}
}

// The scheduler, the send plan and previews all read the resolved pool, so the
// campaign's aliases and the hold have to ride on the accounts it returns.
func TestResolveCampaignSenderPoolCarriesCampaignSendAs(t *testing.T) {
	verified := models.Email{ID: uuid.New(), Email: "a@acme.test"}
	held := models.Email{ID: uuid.New(), Email: "b@acme.test"}
	plain := models.Email{ID: uuid.New(), Email: "c@acme.test", SendAsEmail: "c.alias@acme.test"}
	tagged := models.Email{ID: uuid.New(), Email: "d@acme.test"}
	src := &stubSenderSource{
		explicit: []CampaignSenderAccount{
			{Account: verified, SendAsEmails: []string{"hello@acme.test", "hi@acme.test"}},
			{Account: held, SendAsEmails: []string{"gone@acme.test"}, SendAsUnverified: []string{"gone@acme.test"}},
			{Account: plain},
		},
		tagged: []models.Email{tagged},
	}
	campaign := testCampaign("explicit")
	campaign.EmailTags = []string{"tag"}

	pool, err := ResolveCampaignSenderPool(context.Background(), src, campaign)
	if err != nil {
		t.Fatalf("ResolveCampaignSenderPool: %v", err)
	}
	byID := map[uuid.UUID]models.Email{}
	for _, a := range pool.Accounts {
		byID[a.ID] = a
	}
	if a := byID[verified.ID]; a.SendFrom() != "hello@acme.test" || a.CampaignSendAsHeld {
		t.Errorf("verified aliases resolved to from=%q held=%v", a.SendFrom(), a.CampaignSendAsHeld)
	}
	if a := byID[held.ID]; !a.CampaignSendAsHeld || a.SendFrom() != "gone@acme.test" {
		t.Errorf("unverified alias resolved to from=%q held=%v, want held on the alias", a.SendFrom(), a.CampaignSendAsHeld)
	}
	if a := byID[plain.ID]; a.SendFrom() != "c.alias@acme.test" || a.CampaignSendAsHeld {
		t.Errorf("no campaign alias resolved to from=%q held=%v, want the mailbox's own alias", a.SendFrom(), a.CampaignSendAsHeld)
	}
	if a := byID[tagged.ID]; a.SendFrom() != "d@acme.test" || a.CampaignSendAsHeld {
		t.Errorf("tag-resolved mailbox resolved to from=%q held=%v", a.SendFrom(), a.CampaignSendAsHeld)
	}
}

func TestNormalizeCampaignSendAs(t *testing.T) {
	got, xerr := normalizeCampaignSendAs([]string{" a@acme.test ", "", "A@acme.test", "b@acme.test"})
	if xerr != nil || len(got) != 2 || got[0] != "a@acme.test" || got[1] != "b@acme.test" {
		t.Fatalf("normalize = %v, %v; want [a@acme.test b@acme.test]", got, xerr)
	}
	if _, xerr := normalizeCampaignSendAs([]string{"a@acme.test\r\nBcc: x@evil.test"}); xerr == nil {
		t.Fatal("a header-injecting alias was accepted")
	}
}
