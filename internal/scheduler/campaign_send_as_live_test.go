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

// A lead already written to from a mailbox whose campaign send-as address
// Gmail stopped verifying waits for that mailbox: its follow-up neither goes
// out from another mailbox nor from the mailbox's own address.
//
//	WARMBLY_TEST_DB=postgres://... go test ./internal/scheduler/ -run LiveCampaignSendAs -v
func TestLiveCampaignSendAsUnverifiedFollowUpWaits(t *testing.T) {
	for _, verified := range []bool{false, true} {
		t.Run(map[bool]string{false: "unverified waits", true: "verified sends"}[verified], func(t *testing.T) {
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
			          send_as = jsonb_build_array(jsonb_build_object('email', 'sales@acme.test', 'verified', $2::boolean))
			      WHERE id = $1`, f.mailbox, verified)
			exec(`UPDATE campaigns SET sender_strategy = 'explicit' WHERE id = $1`, f.campaign)
			exec(`INSERT INTO campaign_senders (campaign_id, email_account_id, weight, enabled, send_as_email)
			      VALUES ($1, $2, 1, true, 'sales@acme.test'), ($1, $3, 1, true, '')`, f.campaign, f.mailbox, other)

			_, pair, accountID, err := sendAsScheduler(t, handle, pool).CalculateNextCampaignTime(ctx, f.campaign)
			if verified {
				if err != nil || pair == nil || accountID != f.mailbox {
					t.Fatalf("pair=%v account=%s err=%v, want the follow-up from its own mailbox", pair, accountID, err)
				}
				return
			}
			if pair != nil || !errors.Is(err, ErrCampaignDeferred) {
				t.Fatalf("pair=%v account=%s err=%v, want the follow-up held for its own mailbox", pair, accountID, err)
			}
			// The wait is explained as the alias, not as a spent mailbox.
			var named int
			if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM campaign_logs WHERE campaign_id = $1 AND (
			          (event_type = 'sender_busy' AND metadata->>'reason' = $2)
			          OR (event_type = 'awaiting_next_step' AND metadata ? 'send_as_unverified'))`,
				f.campaign, gateSendAs).Scan(&named); err != nil {
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
