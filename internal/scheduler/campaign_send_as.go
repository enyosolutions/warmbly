package scheduler

import (
	"context"

	"github.com/google/uuid"
	"github.com/warmbly/warmbly/internal/models"
)

// leadSendAsCounter counts leads waiting on an unverified alias.
type leadSendAsCounter interface {
	CountLeadsWaitingOnSendAs(ctx context.Context, campaignID uuid.UUID) (int, error)
}

// heldSendAsMailboxes names the mailboxes closed to new leads by gateSendAs.
func heldSendAsMailboxes(accounts []models.Email) []string {
	var held []string
	for _, a := range accounts {
		if a.CampaignSendAsHeld {
			held = append(held, a.Email)
		}
	}
	return held
}
