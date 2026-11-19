package repository

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/warmbly/warmbly/internal/errx"
	"github.com/warmbly/warmbly/internal/models"
)

// A campaign's send-as aliases per mailbox: validated on save, kept when an
// unrelated save omits them, and never cleared when Gmail stops verifying
// one, so the mailbox is held instead of falling back to another address.
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
	              '[{"email":"hello@acme.test","verified":true},{"email":"hi@acme.test","verified":true},{"email":"pending@acme.test","verified":false}]'::jsonb)`,
		gmail, f.owner, f.org, gmail.String()+"@acme.test")
	exec(`INSERT INTO email_accounts (id, user_id, organization_id, email, name, signature_plain, signature_html, provider, status)
	      VALUES ($1, $2, $3, $4, 'Sam', '', '', 'smtp_imap', 'active')`, smtp, f.owner, f.org, smtp.String()+"@acme.test")

	list := func(a ...string) *[]string { return &a }
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

	// Refused: a list holding one alias Gmail has not verified, and any alias
	// on SMTP/IMAP.
	if _, xerr := campaigns.ReplaceCampaignSenders(ctx, f.campaign, []models.CampaignSenderInput{
		{EmailAccountID: gmail, SendAsEmails: list("hello@acme.test", "pending@acme.test")},
	}); !errors.Is(xerr, errx.ErrEmailSendAsUnknown) {
		t.Fatalf("list with an unverified alias saved: %v", xerr)
	}
	if _, xerr := campaigns.ReplaceCampaignSenders(ctx, f.campaign, []models.CampaignSenderInput{
		{EmailAccountID: smtp, SendAsEmails: list("hello@acme.test")},
	}); !errors.Is(xerr, errx.ErrEmailSendAsUnsupported) {
		t.Fatalf("alias on a non-Gmail mailbox saved: %v", xerr)
	}

	// Saved trimmed and deduplicated, all verified.
	senders, xerr := campaigns.ReplaceCampaignSenders(ctx, f.campaign, []models.CampaignSenderInput{
		{EmailAccountID: gmail, SendAsEmails: list("Hello@Acme.test", " hi@acme.test ", "hello@acme.test")},
		{EmailAccountID: smtp},
	})
	if xerr != nil {
		t.Fatalf("save verified aliases: %v", xerr)
	}
	if s := find(senders, gmail); len(s.SendAsEmails) != 2 || s.SendAsEmails[0] != "Hello@Acme.test" || s.SendAsEmails[1] != "hi@acme.test" || len(s.SendAsUnverified) != 0 {
		t.Fatalf("saved aliases read back as %+v", s)
	}
	if s := find(senders, smtp); len(s.SendAsEmails) != 0 || len(s.SendAsUnverified) != 0 {
		t.Fatalf("mailbox without aliases read back as %+v", s)
	}

	// A save that omits the field (every older client) keeps the list.
	senders, xerr = campaigns.ReplaceCampaignSenders(ctx, f.campaign, []models.CampaignSenderInput{
		{EmailAccountID: gmail}, {EmailAccountID: smtp},
	})
	if xerr != nil {
		t.Fatalf("save without alias field: %v", xerr)
	}
	if s := find(senders, gmail); len(s.SendAsEmails) != 2 {
		t.Fatalf("omitted field cleared the aliases: %+v", s)
	}

	// Gmail stops verifying one: it stays listed, reported unverified, and the
	// mailbox stays open to new leads on the other one.
	exec(`UPDATE email_accounts SET send_as = '[{"email":"hello@acme.test","verified":false},{"email":"hi@acme.test","verified":true}]'::jsonb WHERE id = $1`, gmail)
	senders, _ = campaigns.GetCampaignSenders(ctx, f.campaign)
	if s := find(senders, gmail); len(s.SendAsEmails) != 2 || len(s.SendAsUnverified) != 1 || s.SendAsUnverified[0] != "Hello@Acme.test" {
		t.Fatalf("partly revoked aliases read back as %+v", s)
	}
	newLead, err := campaigns.(*campaignRepository).CampaignLeadSendAs(ctx, f.campaign, uuid.New(), gmail)
	if err != nil || newLead.BoundHere || newLead.Configured != 2 || len(newLead.Verified) != 1 || newLead.Verified[0] != "hi@acme.test" {
		t.Fatalf("send-time read for a new lead: %+v err=%v", newLead, err)
	}

	// Gmail stops verifying both: the scheduler's read holds the mailbox on
	// one of the campaign's aliases, never the mailbox address.
	exec(`UPDATE email_accounts SET send_as = '[]'::jsonb WHERE id = $1`, gmail)
	explicit, xerr := emails.GetByCampaignSenders(ctx, NewAccountScope(&f.org), f.campaign)
	if xerr != nil {
		t.Fatalf("GetByCampaignSenders: %v", xerr)
	}
	held := false
	for _, s := range explicit {
		if s.Account.ID == gmail {
			acct := ApplyCampaignSendAs(s.Account, s.SendAsEmails, s.SendAsUnverified)
			held = acct.CampaignSendAsHeld && acct.SendFrom() == "Hello@Acme.test"
		}
	}
	if !held {
		t.Fatalf("scheduler read did not hold the mailbox on a campaign alias: %+v", explicit)
	}

	// Re-saving with the stale list omitted still works; clearing it works.
	if _, xerr := campaigns.ReplaceCampaignSenders(ctx, f.campaign, []models.CampaignSenderInput{
		{EmailAccountID: gmail}, {EmailAccountID: smtp},
	}); xerr != nil {
		t.Fatalf("unrelated save failed on stale aliases: %v", xerr)
	}
	senders, xerr = campaigns.ReplaceCampaignSenders(ctx, f.campaign, []models.CampaignSenderInput{
		{EmailAccountID: gmail, SendAsEmails: list()}, {EmailAccountID: smtp},
	})
	if xerr != nil {
		t.Fatalf("clear aliases: %v", xerr)
	}
	if s := find(senders, gmail); len(s.SendAsEmails) != 0 || len(s.SendAsUnverified) != 0 {
		t.Fatalf("cleared aliases read back as %+v", s)
	}
}
