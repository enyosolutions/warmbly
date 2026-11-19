package tasks

import (
	"context"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/models"
	"github.com/warmbly/warmbly/internal/repository"
)

// A campaign's per-mailbox send-as aliases: each new lead gets one, keeps it
// for its follow-ups, and never falls back to another address.
//
//	WARMBLY_TEST_DB=postgres://... go test ./internal/tasks/ -run LiveCampaignSendAs -v

// fromRecorder keeps the mailbox exactly as each send was handed it.
type fromRecorder struct {
	mu   sync.Mutex
	sent map[uuid.UUID]models.Email
}

func (s *fromRecorder) Send(_ context.Context, taskID uuid.UUID, _ EmailMessage, account models.Email) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sent == nil {
		s.sent = map[uuid.UUID]models.Email{}
	}
	s.sent[taskID] = account
	return nil
}

func (s *fromRecorder) get(taskID uuid.UUID) (models.Email, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.sent[taskID]
	return a, ok
}

// sendAsFixture puts the rotation fixture's two mailboxes on the campaign's
// explicit pool as Gmail: the first lists two verified aliases, the second one
// alias Gmail no longer verifies.
func sendAsFixture(t *testing.T, leads int) (*rotationFixture, *fromRecorder) {
	t.Helper()
	f := newRotationFixture(t, leads)
	rec := &fromRecorder{}
	f.svc.emailSender = rec
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := f.pool.Exec(context.Background(), sql, args...); err != nil {
			t.Fatalf("fixture %q: %v", sql[:min(60, len(sql))], err)
		}
	}
	t.Cleanup(func() {
		_, _ = f.pool.Exec(context.Background(), `DELETE FROM campaign_senders WHERE campaign_id = $1`, f.campaign)
	})
	exec(`UPDATE email_accounts SET provider = 'gmail',
	          send_as = '[{"email":"sales@acme.test","verified":true},{"email":"team@acme.test","verified":true}]'::jsonb WHERE id = $1`, f.mailboxes[0])
	exec(`UPDATE email_accounts SET provider = 'gmail',
	          send_as = '[{"email":"gone@acme.test","verified":false}]'::jsonb WHERE id = $1`, f.mailboxes[1])
	exec(`UPDATE campaigns SET sender_strategy = 'explicit' WHERE id = $1`, f.campaign)
	exec(`INSERT INTO campaign_senders (campaign_id, email_account_id, weight, enabled, send_as_emails)
	      VALUES ($1, $2, 1, true, '{sales@acme.test,team@acme.test}'), ($1, $3, 1, true, '{gone@acme.test}')`,
		f.campaign, f.mailboxes[0], f.mailboxes[1])
	return f, rec
}

func leadSendAs(t *testing.T, f *rotationFixture, mailbox uuid.UUID) map[string]int {
	t.Helper()
	rows, err := f.pool.Query(context.Background(), `
		SELECT send_as_email, COUNT(*) FROM campaign_leads
		WHERE campaign_id = $1 AND email_account_id = $2 GROUP BY 1`, f.campaign, mailbox)
	if err != nil {
		t.Fatalf("read lead aliases: %v", err)
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var a string
		var n int
		if err := rows.Scan(&a, &n); err != nil {
			t.Fatalf("scan: %v", err)
		}
		out[a] = n
	}
	return out
}

func TestLiveCampaignSendAsNewLeadsDrawAVerifiedAliasAndKeepIt(t *testing.T) {
	f, rec := sendAsFixture(t, 4)

	for i := 0; i < 4; i++ {
		taskID := f.tick(t, f.mailboxes[1])
		acct, ok := rec.get(taskID)
		if !ok {
			t.Fatalf("tick %d dispatched nothing; the mailbox with verified aliases should have taken the lead", i+1)
		}
		if acct.ID != f.mailboxes[0] {
			t.Fatalf("tick %d started a lead on %s, whose only alias is unverified", i+1, acct.ID)
		}
		if from := acct.SendFrom(); from != "sales@acme.test" && from != "team@acme.test" {
			t.Fatalf("tick %d sent as %q, want one of the campaign aliases", i+1, from)
		}
	}
	// Each lead records the alias its first email used, and only those two.
	got := leadSendAs(t, f, f.mailboxes[0])
	if got["sales@acme.test"]+got["team@acme.test"] != 4 || len(got) > 2 {
		t.Fatalf("lead aliases %v, want 4 leads on sales@ or team@", got)
	}
}

// A lead already on a mailbox keeps the alias its first email used, even one
// removed from the campaign's list, for as long as Gmail verifies it.
func TestLiveCampaignSendAsFollowUpKeepsItsAlias(t *testing.T) {
	f, rec := sendAsFixture(t, 1)
	ctx := context.Background()
	repo := f.svc.campaignRepo.(interface {
		CampaignLeadSendAs(ctx context.Context, campaignID, contactID, emailAccountID uuid.UUID) (repository.CampaignLeadSendAs, error)
	})

	taskID := f.tick(t, f.mailboxes[1])
	first, ok := rec.get(taskID)
	if !ok {
		t.Fatal("first tick dispatched nothing")
	}
	var contact uuid.UUID
	if err := f.pool.QueryRow(ctx, `SELECT contact_id FROM campaign_leads WHERE campaign_id = $1`, f.campaign).Scan(&contact); err != nil {
		t.Fatalf("read lead: %v", err)
	}
	// Drop the alias it got from the list: the lead keeps it.
	if _, err := f.pool.Exec(ctx, `UPDATE campaign_senders SET send_as_emails = array_remove(send_as_emails, $3)
	      WHERE campaign_id = $1 AND email_account_id = $2`, f.campaign, f.mailboxes[0], first.SendFrom()); err != nil {
		t.Fatalf("remove alias: %v", err)
	}
	sa, err := repo.CampaignLeadSendAs(ctx, f.campaign, contact, f.mailboxes[0])
	if err != nil {
		t.Fatalf("CampaignLeadSendAs: %v", err)
	}
	alias, held := pickLeadSendAs(sa, func(n int) int { return n - 1 })
	if held || alias != first.SendFrom() {
		t.Fatalf("follow-up picked %q held=%v, want the first email's %q", alias, held, first.SendFrom())
	}

	// Gmail stops verifying it: the follow-up waits rather than change address.
	if _, err := f.pool.Exec(ctx, `UPDATE email_accounts SET send_as = '[]'::jsonb WHERE id = $1`, f.mailboxes[0]); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	sa, _ = repo.CampaignLeadSendAs(ctx, f.campaign, contact, f.mailboxes[0])
	if _, held := pickLeadSendAs(sa, func(n int) int { return 0 }); !held {
		t.Fatal("a follow-up on a revoked alias was not held")
	}
}

// heldAtSend reports every alias as unverified at the moment of the send, as
// if Gmail revoked them between the scheduler's pass and the send.
type heldAtSend struct {
	repository.CampaignRepository
}

func (heldAtSend) CampaignLeadSendAs(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) (repository.CampaignLeadSendAs, error) {
	return repository.CampaignLeadSendAs{Configured: 2}, nil
}

func TestLiveCampaignSendAsRevokedBeforeTheSendIsHeldNotSent(t *testing.T) {
	f, rec := sendAsFixture(t, 1)
	f.svc.campaignRepo = heldAtSend{CampaignRepository: f.svc.campaignRepo}

	taskID := f.tick(t, f.mailboxes[0])
	if acct, ok := rec.get(taskID); ok {
		t.Fatalf("a send held at send time still went out, as %q", acct.SendFrom())
	}
	ctx := context.Background()
	var status string
	if err := f.pool.QueryRow(ctx, `SELECT status FROM tasks WHERE id = $1`, taskID).Scan(&status); err != nil {
		t.Fatalf("read task: %v", err)
	}
	if status != "completed" {
		t.Fatalf("held task status %q, want completed without a send", status)
	}
	var progress int
	if err := f.pool.QueryRow(ctx, `SELECT COUNT(*) FROM campaign_contact_progress WHERE campaign_id = $1`, f.campaign).Scan(&progress); err != nil {
		t.Fatalf("read progress: %v", err)
	}
	if progress != 0 {
		t.Fatalf("held send recorded %d progress rows; the lead must still be due", progress)
	}
	var next int
	if err := f.pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM tasks t JOIN campaign_tasks ct ON ct.task_id = t.id
		WHERE ct.campaign_id = $1 AND t.status = 'pending' AND t.id <> $2`, f.campaign, taskID).Scan(&next); err != nil {
		t.Fatalf("read next pass: %v", err)
	}
	if next != 1 {
		t.Fatalf("held send scheduled %d next passes, want 1", next)
	}
	var logged int
	if err := f.pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM campaign_logs WHERE campaign_id = $1 AND event_type = 'sender_busy'
		  AND metadata->>'reason' = 'send_as_unverified'`, f.campaign).Scan(&logged); err != nil {
		t.Fatalf("read log: %v", err)
	}
	if logged == 0 {
		t.Fatal("held send left no campaign log entry")
	}
}
