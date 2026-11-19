package scheduler

import "github.com/warmbly/warmbly/internal/models"

// heldSendAsMailboxes names the pool's mailboxes held because the campaign's
// send-as address on them is no longer verified (see gateSendAs).
func heldSendAsMailboxes(accounts []models.Email) []string {
	var held []string
	for _, a := range accounts {
		if a.CampaignSendAsHeld {
			held = append(held, a.Email)
		}
	}
	return held
}
