package repository

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/models"
)

func TestApplyCampaignSendAs(t *testing.T) {
	mailbox := models.Email{ID: uuid.New(), Email: "jane@acme.test", SendAsEmail: "jane.alias@acme.test"}

	if got := ApplyCampaignSendAs(mailbox, "  ", false); got.SendAsEmail != mailbox.SendAsEmail || got.CampaignSendAsHeld {
		t.Fatalf("no campaign alias changed the mailbox: %+v", got)
	}
	if got := ApplyCampaignSendAs(mailbox, "sales@acme.test", true); got.SendAsEmail != "sales@acme.test" || got.CampaignSendAsHeld {
		t.Fatalf("verified campaign alias: from %q held %v, want sales@acme.test and not held", got.SendAsEmail, got.CampaignSendAsHeld)
	}
	// The guard this feature rests on: an unverified alias holds the mailbox
	// and keeps the alias, so nothing can fall back to another address.
	got := ApplyCampaignSendAs(mailbox, "sales@acme.test", false)
	if !got.CampaignSendAsHeld {
		t.Fatal("an unverified campaign alias did not hold the mailbox")
	}
	if got.SendAsEmail != "sales@acme.test" || got.SendFrom() != "sales@acme.test" {
		t.Fatalf("held mailbox would send from %q, want the campaign alias kept", got.SendFrom())
	}
	if mailbox.SendAsEmail != "jane.alias@acme.test" || mailbox.CampaignSendAsHeld {
		t.Fatal("ApplyCampaignSendAs changed the caller's mailbox")
	}
}

// The scheduler, the send plan and previews all read the resolved pool, so the
// campaign alias and its hold have to ride on the accounts it returns.
func TestResolveCampaignSenderPoolCarriesCampaignSendAs(t *testing.T) {
	verified := models.Email{ID: uuid.New(), Email: "a@acme.test"}
	held := models.Email{ID: uuid.New(), Email: "b@acme.test"}
	plain := models.Email{ID: uuid.New(), Email: "c@acme.test", SendAsEmail: "c.alias@acme.test"}
	tagged := models.Email{ID: uuid.New(), Email: "d@acme.test"}
	src := &stubSenderSource{
		explicit: []CampaignSenderAccount{
			{Account: verified, SendAsEmail: "hello@acme.test", SendAsVerified: true},
			{Account: held, SendAsEmail: "gone@acme.test", SendAsVerified: false},
			{Account: plain, SendAsVerified: true},
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
		t.Errorf("verified alias resolved to from=%q held=%v", a.SendFrom(), a.CampaignSendAsHeld)
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
