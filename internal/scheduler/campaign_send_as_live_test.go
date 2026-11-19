package scheduler

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/warmbly/warmbly/internal/app/behavior"
	"github.com/warmbly/warmbly/internal/infrastructure/db"
	"github.com/warmbly/warmbly/internal/models"
	"github.com/warmbly/warmbly/internal/repository"
)

// sendAsScheduler is liveScheduler with the campaign log wired, so a waiting
// lead's reason can be read back.
func sendAsScheduler(t *testing.T, handle *db.DB, pool *pgxpool.Pool) SchedulerService {
	t.Helper()
	emailRepo := repository.NewEmailRepostory(handle, testEncrypter(t))
	s := NewSchedulerService(
		repository.NewTaskRepository(pool),
		repository.NewWarmupRepository(pool),
		repository.NewCampaignProgressRepository(pool),
		emailRepo,
		repository.NewCampaignRepostory(handle),
		repository.NewContactRepostory(handle),
		repository.NewCampaignLogRepository(handle),
	)
	if aware, ok := s.(BehaviorAware); ok {
		aware.WireBehavior(behavior.NewService(repository.NewBehaviorRepository(pool), emailRepo))
	}
	return s
}

// A lead mid-sequence waits only on its own send-as address: never on the
// campaign's list for its mailbox, which governs new leads.
//
//	WARMBLY_TEST_DB=postgres://... go test ./internal/scheduler/ -run LiveCampaignSendAs -v
func TestLiveCampaignSendAsFollowUpWaitsOnlyOnItsOwnAlias(t *testing.T) {
	cases := []struct {
		name      string
		verified  []string // aliases Gmail verifies on the lead's mailbox
		list      string   // the campaign's alias list for it
		leadAlias string   // the alias the lead's first email used
		wantWait  bool
	}{
		{"its alias revoked, another still verified", []string{"a@acme.test"}, "{a@acme.test,b@acme.test}", "b@acme.test", true},
		{"its alias removed from the list but verified", []string{"a@acme.test"}, "{b@acme.test}", "a@acme.test", false},
		{"mailbox closed to new leads, lead on the mailbox address", nil, "{b@acme.test}", "", false},
		{"its alias verified", []string{"a@acme.test", "b@acme.test"}, "{a@acme.test,b@acme.test}", "b@acme.test", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			handle, pool := liveDB(t)
			f := newFollowUpFixture(t, pool, models.FollowUpSameMailbox, 2*time.Hour)
			other := f.addMailbox(t, "other.test", 50, 0)
			ctx := context.Background()
			exec := func(sql string, args ...any) {
				t.Helper()
				if _, err := pool.Exec(ctx, sql, args...); err != nil {
					t.Fatalf("fixture %q: %v", sql[:min(60, len(sql))], err)
				}
			}
			t.Cleanup(func() {
				c := context.Background()
				_, _ = pool.Exec(c, `DELETE FROM campaign_senders WHERE campaign_id = $1`, f.campaign)
				_, _ = pool.Exec(c, `DELETE FROM campaign_logs WHERE campaign_id = $1`, f.campaign)
			})
			exec(`UPDATE email_accounts SET provider = 'gmail',
			          send_as = COALESCE((SELECT jsonb_agg(jsonb_build_object('email', e, 'verified', true)) FROM unnest($2::text[]) AS e), '[]'::jsonb)
			      WHERE id = $1`, f.mailbox, c.verified)
			exec(`UPDATE campaigns SET sender_strategy = 'explicit' WHERE id = $1`, f.campaign)
			exec(`INSERT INTO campaign_senders (campaign_id, email_account_id, weight, enabled, send_as_emails)
			      VALUES ($1, $2, 1, true, $4::text[]), ($1, $3, 1, true, '{}')`, f.campaign, f.mailbox, other, c.list)
			exec(`UPDATE campaign_leads SET send_as_email = $3 WHERE campaign_id = $1 AND contact_id = $2`, f.campaign, f.contact, c.leadAlias)

			_, pair, accountID, err := sendAsScheduler(t, handle, pool).CalculateNextCampaignTime(ctx, f.campaign)
			if !c.wantWait {
				if err != nil || pair == nil || accountID != f.mailbox {
					t.Fatalf("pair=%v account=%s err=%v, want the follow-up from its own mailbox", pair, accountID, err)
				}
				return
			}
			if pair != nil || !errors.Is(err, ErrCampaignDeferred) {
				t.Fatalf("pair=%v account=%s err=%v, want the follow-up waiting on its alias", pair, accountID, err)
			}
			var named int
			if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM campaign_logs WHERE campaign_id = $1
			      AND event_type = 'awaiting_next_step' AND metadata ? 'send_as_unverified_leads'`, f.campaign).Scan(&named); err != nil {
				t.Fatalf("read log: %v", err)
			}
			if named == 0 {
				t.Fatal("the waiting lead's log does not name the unverified send-as address")
			}
			var bound uuid.UUID
			if err := pool.QueryRow(ctx, `SELECT email_account_id FROM campaign_leads WHERE campaign_id = $1 AND contact_id = $2`,
				f.campaign, f.contact).Scan(&bound); err != nil {
				t.Fatalf("read binding: %v", err)
			}
			if bound != f.mailbox {
				t.Fatalf("lead moved to %s, want it kept on its own mailbox", bound)
			}
		})
	}
}
