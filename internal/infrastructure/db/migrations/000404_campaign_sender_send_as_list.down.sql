UPDATE campaign_senders
SET send_as_email = send_as_emails[1]
WHERE cardinality(send_as_emails) > 0 AND send_as_email = '';

ALTER TABLE campaign_senders
    DROP COLUMN IF EXISTS send_as_emails;

ALTER TABLE campaign_leads
    DROP COLUMN IF EXISTS send_as_email;
