-- A campaign can send from a Gmail send-as alias of each of its mailboxes,
-- chosen per (campaign, mailbox) on the explicit sender pool. Empty, the
-- default, keeps the mailbox's own choice (email_accounts.send_as_email, or
-- the mailbox address when that is empty too).
--
-- The value is never cleared when the provider stops verifying it: an empty
-- value means "fall back to the mailbox", and a campaign that named an alias
-- must not quietly start writing from another address. The scheduler holds the
-- mailbox for this campaign instead, and its leads wait, until the alias is
-- verified again or the campaign picks another one.
--
-- Additive with a constant default: the previous release ignores the column.
ALTER TABLE campaign_senders
    ADD COLUMN IF NOT EXISTS send_as_email text NOT NULL DEFAULT '';
