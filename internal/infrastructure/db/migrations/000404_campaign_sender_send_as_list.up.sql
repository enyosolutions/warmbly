-- A campaign can now hold several send-as aliases per mailbox. Each new lead
-- gets one of them at random on its first email, and keeps it for the rest of
-- its sequence, so a contact always hears from the same address.
--
-- campaign_senders.send_as_emails replaces send_as_email (000403) as the one
-- field the application reads and writes. An empty list keeps the mailbox's
-- own choice. send_as_email is copied over and left in place, unused, so the
-- release that wrote it keeps working during a rollout; a later migration can
-- drop it.
ALTER TABLE campaign_senders
    ADD COLUMN IF NOT EXISTS send_as_emails text[] NOT NULL DEFAULT '{}';

UPDATE campaign_senders
SET send_as_emails = ARRAY[send_as_email]
WHERE send_as_email <> '' AND send_as_emails = '{}';

-- The alias a lead's sequence leaves from, chosen with its mailbox
-- (campaign_leads.email_account_id) when its first email is reserved, and
-- cleared with it. Empty is the mailbox's own choice, which is also what
-- every lead bound before this column existed keeps.
ALTER TABLE campaign_leads
    ADD COLUMN IF NOT EXISTS send_as_email text NOT NULL DEFAULT '';
