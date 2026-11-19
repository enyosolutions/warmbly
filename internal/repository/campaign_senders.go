package repository

import (
	"context"
	"strings"

	"github.com/google/uuid"
	"github.com/warmbly/warmbly/internal/errx"
	"github.com/warmbly/warmbly/internal/models"
)

// CampaignSenderPool is the set of mailboxes a campaign sends from, resolved
// the one way every caller must agree on: the explicit campaign_senders pool
// and the tag-resolved mailboxes united, and when the campaign selects
// neither, every active mailbox in its organization ("all").
type CampaignSenderPool struct {
	// Accounts is the union, in explicit-pool-first order, without duplicates.
	Accounts []models.Email
	// Explicit is the campaign_senders pool with its rotation metadata.
	Explicit []CampaignSenderAccount
}

// CampaignSenderSource is the slice of EmailRepository the pool resolver needs.
// Narrow on purpose: the resolver is the one place three callers must agree on,
// and a three-method dependency is one a test can stand up.
type CampaignSenderSource interface {
	GetByCampaignSenders(ctx context.Context, scope AccountScope, campaignID uuid.UUID) ([]CampaignSenderAccount, *errx.Error)
	GetByTags(ctx context.Context, scope AccountScope, tags []string) ([]models.Email, *errx.Error)
	GetAllActiveInScope(ctx context.Context, scope AccountScope) ([]models.Email, *errx.Error)
}

// ResolveCampaignSenderPool resolves a campaign's mailboxes. The scheduler and
// the pre-send checks both go through it, so a check can never refuse a pool
// the scheduler would happily send from (issue #340: a campaign on the "all"
// fallback was told it had no sender accounts).
//
// Tenancy is the campaign's organization, never its owner: a user in two
// organizations must not have A's campaign pick up B's mailbox. A campaign
// with no organization resolves to no mailboxes.
func ResolveCampaignSenderPool(ctx context.Context, repo CampaignSenderSource, campaign *models.Campaign) (CampaignSenderPool, *errx.Error) {
	pool := CampaignSenderPool{Accounts: []models.Email{}}
	scope := NewAccountScope(campaign.OrganizationID)
	explicit, err := repo.GetByCampaignSenders(ctx, scope, campaign.ID)
	if err != nil {
		return pool, err
	}
	// The campaign's own alias rides on the explicit pool's copies, so every
	// caller (the scheduler's gates, the send plan, previews) sees the address
	// this campaign actually sends as, and which mailboxes it holds.
	for i := range explicit {
		explicit[i].Account = ApplyCampaignSendAs(explicit[i].Account, explicit[i].SendAsEmail, explicit[i].SendAsVerified)
	}
	pool.Explicit = explicit
	seen := map[string]bool{}
	for _, snd := range explicit {
		pool.Accounts = append(pool.Accounts, snd.Account)
		seen[snd.Account.ID.String()] = true
	}
	if len(campaign.EmailTags) > 0 {
		tagged, err := repo.GetByTags(ctx, scope, campaign.EmailTags)
		if err != nil {
			return pool, err
		}
		for _, acct := range tagged {
			if !seen[acct.ID.String()] {
				pool.Accounts = append(pool.Accounts, acct)
				seen[acct.ID.String()] = true
			}
		}
	}
	if len(explicit) == 0 && len(campaign.EmailTags) == 0 && !ExplicitSenderPool(campaign) {
		all, err := repo.GetAllActiveInScope(ctx, scope)
		if err != nil {
			return pool, err
		}
		pool.Accounts = all
	}
	return pool, nil
}

// ApplyCampaignSendAs returns the mailbox as one campaign sends from it. An
// empty campaign alias leaves the mailbox's own choice. A set one replaces it
// when verified, and otherwise marks the mailbox held: the alias is kept, so a
// held mailbox can never quietly send from the mailbox address instead.
func ApplyCampaignSendAs(acct models.Email, alias string, verified bool) models.Email {
	alias = strings.TrimSpace(alias)
	if alias == "" {
		return acct
	}
	acct.SendAsEmail = alias
	acct.CampaignSendAsHeld = !verified
	return acct
}

// CampaignSenderStrategyExplicit is the campaigns.sender_strategy value that
// means "these mailboxes and no others".
const CampaignSenderStrategyExplicit = "explicit"

// ExplicitSenderPool reports whether a campaign named its mailboxes by hand.
//
// It is what stops the "all active mailboxes" fallback from widening a
// campaign that asked for three mailboxes into one sending from every mailbox
// in the workspace. An explicit pool can empty out on its own (the mailboxes
// are disconnected, or the pool is replaced with nothing) and before this the
// campaign silently carried on from every address the tenant owns.
//
// The tag union above still applies, which is what migration 000013 designed:
// an explicit campaign that also carries tags falls back to those. What it can
// no longer do is fall back to the whole workspace. With neither it resolves to
// no mailboxes, which parks it as paused_no_accounts with a reason in its
// activity log.
//
// Only 'explicit' is special-cased: 'tags' is the default and the value the
// dashboard writes, so nothing about the existing tag or "all" behaviour moves.
func ExplicitSenderPool(campaign *models.Campaign) bool {
	return campaign != nil && campaign.SenderStrategy == CampaignSenderStrategyExplicit
}
