package tasks

import (
	"context"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/models"
	"github.com/warmbly/warmbly/internal/repository"
)

// A campaign's per-mailbox send-as address goes out as the From address, and
// a mailbox whose address Gmail no longer verifies sends nothing for that
// campaign: it never falls back to another address.
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

// sendAsFixture turns the rotation fixture's two mailboxes into Gmail ones on
// the campaign's explicit pool: the first with a verified campaign alias, the
// second with one Gmail no longer verifies.
func sendAsFixture(t *testing.T, leads int) (*rotationFixture, *fromRecorder) {
	t.Helper()
	f := newRotationFixture(t, leads)
	rec := &fromRecorder{}
	f.svc.emailSender = rec
	ctx := context.Background()
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := f.pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("fixture %q: %v", sql[:min(60, len(sql))], err)
		}
	}
	t.Cleanup(func() {
		_, _ = f.pool.Exec(context.Background(), `DELETE FROM campaign_senders WHERE campaign_id = $1`, f.campaign)
	})
	exec(`UPDATE email_accounts SET provider = 'gmail',
	          send_as = '[{"email":"sales@acme.test","verified":true}]'::jsonb WHERE id = $1`, f.mailboxes[0])
	exec(`UPDATE email_accounts SET provider = 'gmail',
	          send_as = '[{"email":"gone@acme.test","verified":false}]'::jsonb WHERE id = $1`, f.mailboxes[1])
	exec(`UPDATE campaigns SET sender_strategy = 'explicit' WHERE id = $1`, f.campaign)
	exec(`INSERT INTO campaign_senders (campaign_id, email_account_id, weight, enabled, send_as_email)
	      VALUES ($1, $2, 1, true, 'sales@acme.test'), ($1, $3, 1, true, 'gone@acme.test')`,
		f.campaign, f.mailboxes[0], f.mailboxes[1])
	return f, rec
}

func TestLiveCampaignSendAsIsTheFromAddressAndAHeldMailboxSendsNothing(t *testing.T) {
	f, rec := sendAsFixture(t, 4)

	for i := 0; i < 4; i++ {
		taskID := f.tick(t, f.mailboxes[1])
		acct, ok := rec.get(taskID)
		if !ok {
			t.Fatalf("tick %d dispatched nothing; the verified mailbox should have taken the lead", i+1)
		}
		if acct.ID != f.mailboxes[0] {
			t.Fatalf("tick %d sent from %s, the mailbox whose campaign alias is unverified", i+1, acct.ID)
		}
		if acct.SendAsEmail != "sales@acme.test" || acct.SendFrom() != "sales@acme.test" {
			t.Fatalf("tick %d sent as %q, want the campaign alias", i+1, acct.SendFrom())
		}
	}
}

// heldAtSend reports every campaign alias as unverified at the moment of the
// send, as if Gmail revoked it between the scheduler's pass and the send.
type heldAtSend struct {
	repository.CampaignRepository
}

func (heldAtSend) CampaignSenderSendAs(context.Context, uuid.UUID, uuid.UUID) (string, bool, error) {
	return "sales@acme.test", false, nil
}

func TestLiveCampaignSendAsRevokedBeforeTheSendIsHeldNotSent(t *testing.T) {
	f, rec := sendAsFixture(t, 1)
	f.svc.campaignRepo = heldAtSend{CampaignRepository: f.svc.campaignRepo}

	taskID := f.tick(t, f.mailboxes[0])
	if acct, ok := rec.get(taskID); ok {
		t.Fatalf("a mailbox held at send time still sent, as %q", acct.SendFrom())
	}
	ctx := context.Background()
	var status string
	if err := f.pool.QueryRow(ctx, `SELECT status FROM tasks WHERE id = $1`, taskID).Scan(&status); err != nil {
		t.Fatalf("read task: %v", err)
	}
	if status != "completed" {
		t.Fatalf("held task status %q, want completed without a send", status)
	}
	var sent int
	if err := f.pool.QueryRow(ctx, `SELECT COUNT(*) FROM campaign_contact_progress WHERE campaign_id = $1`, f.campaign).Scan(&sent); err != nil {
		t.Fatalf("read progress: %v", err)
	}
	if sent != 0 {
		t.Fatalf("held send recorded %d progress rows; the lead must still be due", sent)
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
