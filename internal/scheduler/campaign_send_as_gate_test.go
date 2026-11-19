package scheduler

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/warmbly/warmbly/internal/models"
)

// A mailbox whose campaign alias is no longer verified is held for that
// campaign, and paced: the leads it already writes to wait for it instead of
// moving to another mailbox, and so to another From address.
func TestCampaignSendAsHeldMailboxIsAPacedGate(t *testing.T) {
	s := &schedulerService{}
	pass := &campaignPass{health: map[uuid.UUID]healthRead{}, sentToday: map[uuid.UUID]int{}}

	gate, remaining := s.gateFor(context.Background(), pass, models.Email{ID: uuid.New(), CampaignSendAsHeld: true}, 10)
	if gate.reason != gateSendAs || !gate.paced || remaining != 0 {
		t.Fatalf("held mailbox gave %+v with %d left, want a paced %q gate with nothing left", gate, remaining, gateSendAs)
	}
	if !gate.reopensAt.IsZero() {
		t.Fatalf("held mailbox reopens at %v; nothing knows when an alias is verified again", gate.reopensAt)
	}
}

// A standing gate still wins over the alias hold, so a disconnected or
// failing mailbox keeps its own reason and its own handling.
func TestCampaignSendAsHoldComesAfterStandingGates(t *testing.T) {
	id := uuid.New()
	s := &schedulerService{}
	pass := &campaignPass{health: map[uuid.UUID]healthRead{id: {state: models.WarmupHealthBlocked, known: true}}}

	if gate, _ := s.gateFor(context.Background(), pass, models.Email{ID: id, CampaignSendAsHeld: true}, 10); gate.reason != gateHealth {
		t.Fatalf("blocked mailbox with a held alias gave %+v, want the health gate", gate)
	}
}
