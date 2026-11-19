package scheduler

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/warmbly/warmbly/internal/models"
)

// A mailbox with none of its campaign aliases verified is closed to new leads
// only: a lead already on it is still placed there, with the budget it has.
func TestCampaignSendAsHeldMailboxClosesToNewLeadsOnly(t *testing.T) {
	id := uuid.New()
	s := &schedulerService{}
	pass := &campaignPass{health: map[uuid.UUID]healthRead{}, sentToday: map[uuid.UUID]int{}}

	gate, remaining := s.gateFor(context.Background(), pass, models.Email{ID: id, CampaignSendAsHeld: true}, 10)
	if gate.reason != gateSendAs || gate.paced || !gate.newLeadsOnly {
		t.Fatalf("held mailbox gave %+v, want a new-leads-only %q gate", gate, gateSendAs)
	}
	if remaining != 10 {
		t.Fatalf("held mailbox kept %d of its budget, want 10 for the leads already on it", remaining)
	}
	if gate.admits(false) {
		t.Fatal("the gate let a new lead in")
	}
	if !gate.admits(true) {
		t.Fatal("the gate refused a lead already bound to the mailbox")
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
