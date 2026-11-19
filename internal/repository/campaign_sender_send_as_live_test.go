package repository

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/warmbly/warmbly/internal/errx"
	"github.com/warmbly/warmbly/internal/models"
)

// A campaign's send-as address per mailbox: validated on save, kept when an
// unrelated save omits it, and never cleared when Gmail stops verifying it,
// so the mailbox is held instead of falling back to another address.
//
//	WARMBLY_TEST_DB=postgres://... go test ./internal/repository/ -run LiveCampaignSenderSendAs -v
func TestLiveCampaignSenderSendAs(t *testing.T) {
	handle, pool := liveContactDB(t)
	f := newSharedOrgFixture(t, pool)
	ctx := context.Background()
	campaigns := NewCampaignRepostory(handle)
	emails := &emailRepository{DB: handle}

	gmail, smtp := uuid.New(), uuid.New()
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("fixture %q: %v", sql[:min(60, len(sql))], err)
		}
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM campaign_senders WHERE campaign_id = $1`, f.campaign)
		_, _ = pool.Exec(c, `DELETE FROM email_accounts WHERE id = ANY($1)`, []uuid.UUID{gmail, smtp})
	})
	exec(`INSERT INTO email_accounts (id, user_id, organization_id, email, name, signature_plain, signature_html, provider, status, send_as)
	      VALUES ($1, $2, $3, $4, 'Jane', '', '', 'gmail', 'active',
	              '[{"email":"hello@acme.test","name":"Hello","verified":true},{"email":"pending@acme.test","verified":false}]'::jsonb)`,
		gmail, f.owner, f.org, gmail.String()+"@acme.test")
	exec(`INSERT INTO email_accounts (id, user_id, organization_id, email, name, signature_plain, signature_html, provider, status)
	      VALUES ($1, $2, $3, $4, 'Sam', '', '', 'smtp_imap', 'active')`, smtp, f.owner, f.org, smtp.String()+"@acme.test")

	str := func(s string) *string { return &s }
	find := func(senders []models.CampaignSender, id uuid.UUID) models.CampaignSender {
		t.Helper()
		for _, s := range senders {
			if s.EmailAccountID == id {
				return s
			}
		}
		t.Fatalf("sender %s missing from %+v", id, senders)
		return models.CampaignSender{}
	}

	// Refused: an alias Gmail has not verified, and any alias on SMTP/IMAP.
	if _, xerr := campaigns.ReplaceCampaignSenders(ctx, f.campaign, []models.CampaignSenderInput{
		{EmailAccountID: gmail, SendAsEmail: str("pending@acme.test")},
	}); !errors.Is(xerr, errx.ErrEmailSendAsUnknown) {
		t.Fatalf("unverified alias saved: %v", xerr)
	}
	if _, xerr := campaigns.ReplaceCampaignSenders(ctx, f.campaign, []models.CampaignSenderInput{
		{EmailAccountID: smtp, SendAsEmail: str("hello@acme.test")},
	}); !errors.Is(xerr, errx.ErrEmailSendAsUnsupported) {
		t.Fatalf("alias on a non-Gmail mailbox saved: %v", xerr)
	}

	// Saved, matched case-insensitively, reported verified.
	senders, xerr := campaigns.ReplaceCampaignSenders(ctx, f.campaign, []models.CampaignSenderInput{
		{EmailAccountID: gmail, SendAsEmail: str("Hello@Acme.test")},
		{EmailAccountID: smtp},
	})
	if xerr != nil {
		t.Fatalf("save verified alias: %v", xerr)
	}
	if s := find(senders, gmail); s.SendAsEmail != "Hello@Acme.test" || !s.SendAsVerified {
		t.Fatalf("saved alias read back as %+v", s)
	}
	if s := find(senders, smtp); s.SendAsEmail != "" || !s.SendAsVerified {
		t.Fatalf("mailbox without an alias read back as %+v", s)
	}

	// A save that omits the field (every older client) keeps the alias.
	senders, xerr = campaigns.ReplaceCampaignSenders(ctx, f.campaign, []models.CampaignSenderInput{
		{EmailAccountID: gmail}, {EmailAccountID: smtp},
	})
	if xerr != nil {
		t.Fatalf("save without alias field: %v", xerr)
	}
	if s := find(senders, gmail); s.SendAsEmail != "Hello@Acme.test" {
		t.Fatalf("omitted field cleared the alias: %+v", s)
	}

	// Gmail stops verifying it: the alias stays, reported unverified, and the
	// mailbox resolves held on that alias for the scheduler and the send.
	exec(`UPDATE email_accounts SET send_as = '[{"email":"hello@acme.test","verified":false}]'::jsonb WHERE id = $1`, gmail)
	senders, _ = campaigns.GetCampaignSenders(ctx, f.campaign)
	if s := find(senders, gmail); s.SendAsEmail != "Hello@Acme.test" || s.SendAsVerified {
		t.Fatalf("revoked alias read back as %+v, want kept and unverified", s)
	}
	alias, verified, err := campaigns.(*campaignRepository).CampaignSenderSendAs(ctx, f.campaign, gmail)
	if err != nil || alias != "Hello@Acme.test" || verified {
		t.Fatalf("send-time read: alias=%q verified=%v err=%v", alias, verified, err)
	}
	explicit, xerr := emails.GetByCampaignSenders(ctx, NewAccountScope(&f.org), f.campaign)
	if xerr != nil {
		t.Fatalf("GetByCampaignSenders: %v", xerr)
	}
	held := false
	for _, s := range explicit {
		if s.Account.ID == gmail {
			acct := ApplyCampaignSendAs(s.Account, s.SendAsEmail, s.SendAsVerified)
			held = acct.CampaignSendAsHeld && acct.SendFrom() == "Hello@Acme.test"
		}
	}
	if !held {
		t.Fatalf("scheduler read did not hold the mailbox on its alias: %+v", explicit)
	}

	// Re-saving with the stale alias omitted still works; clearing it works.
	if _, xerr := campaigns.ReplaceCampaignSenders(ctx, f.campaign, []models.CampaignSenderInput{
		{EmailAccountID: gmail}, {EmailAccountID: smtp},
	}); xerr != nil {
		t.Fatalf("unrelated save failed on a stale alias: %v", xerr)
	}
	senders, xerr = campaigns.ReplaceCampaignSenders(ctx, f.campaign, []models.CampaignSenderInput{
		{EmailAccountID: gmail, SendAsEmail: str("")}, {EmailAccountID: smtp},
	})
	if xerr != nil {
		t.Fatalf("clear alias: %v", xerr)
	}
	if s := find(senders, gmail); s.SendAsEmail != "" || !s.SendAsVerified {
		t.Fatalf("cleared alias read back as %+v", s)
	}

	// A mailbox with no campaign_senders row has no campaign alias.
	if alias, verified, err := campaigns.(*campaignRepository).CampaignSenderSendAs(ctx, f.campaign, uuid.New()); err != nil || alias != "" || !verified {
		t.Fatalf("no row: alias=%q verified=%v err=%v", alias, verified, err)
	}
}
