// CampaignSendAs: which Gmail send-as address this campaign writes from on each
// of its individually picked mailboxes. One row per picked Gmail mailbox; other
// providers expose no send-as list, so they are listed as using their own
// address. Mailboxes resolved from tags carry no per-campaign choice.
//
// A stored address the provider has stopped verifying stays selected and is
// flagged: the mailbox is held for this campaign until it is verified again or
// another address is picked. It is never silently replaced.

import React from "react";
import { AlertTriangleIcon } from "lucide-react";
import useEmails from "@/lib/api/hooks/app/emails/useEmails";
import useSendIdentity from "@/lib/api/hooks/app/emails/useSendIdentity";
import { SelectMenu, type SelectOption } from "@/components/ui/select-menu";
import type Inbox from "@/lib/api/models/app/emails/Inbox";

export default function CampaignSendAs({
    accountIds,
    sendAs,
    onChange,
    unverified,
}: {
    accountIds: string[];
    sendAs: Record<string, string>;
    onChange: (accountId: string, alias: string) => void;
    /** Saved aliases the provider no longer verifies, by mailbox id. */
    unverified: Record<string, boolean>;
}) {
    const { emails } = useEmails({ query: "", tag: "" });
    const byId = React.useMemo(() => {
        const m = new Map<string, Inbox>();
        for (const e of emails) m.set(e.id, e);
        return m;
    }, [emails]);

    const rows = accountIds.map((id) => byId.get(id)).filter((e): e is Inbox => !!e);
    if (rows.length === 0) return null;

    return (
        <div>
            <div className="text-[11.5px] font-medium text-slate-700 mb-1.5">Send as</div>
            <div className="rounded-md border border-slate-200 divide-y divide-slate-100">
                {rows.map((mailbox) => (
                    <SendAsRow
                        key={mailbox.id}
                        mailbox={mailbox}
                        value={sendAs[mailbox.id] ?? ""}
                        onChange={(v) => onChange(mailbox.id, v)}
                        unverified={!!unverified[mailbox.id]}
                    />
                ))}
            </div>
            <p className="text-[11px] text-slate-400 mt-1.5">
                Gmail mailboxes only. The address must be verified in Gmail&apos;s settings first. If Gmail stops
                verifying it, the mailbox sends nothing for this campaign and its leads wait until it is verified again
                or you pick another address.
            </p>
        </div>
    );
}

function SendAsRow({
    mailbox,
    value,
    onChange,
    unverified,
}: {
    mailbox: Inbox;
    value: string;
    onChange: (v: string) => void;
    unverified: boolean;
}) {
    const isGmail = mailbox.provider === "gmail";
    const identity = useSendIdentity(mailbox.id, isGmail);

    const mailboxDefault = mailbox.send_as_email?.trim() || mailbox.email;
    const options: SelectOption[] = [{ value: "", label: `Mailbox default (${mailboxDefault})` }];
    for (const id of identity.data?.identities ?? []) {
        options.push({
            value: id.email,
            label: id.verified ? (id.name ? `${id.name} · ${id.email}` : id.email) : `${id.email} (not verified)`,
            disabled: !id.verified,
        });
    }
    // A stored choice the provider no longer lists still has to show as selected.
    if (value && !options.some((o) => o.value.toLowerCase() === value.toLowerCase())) {
        options.push({ value, label: `${value} (no longer listed)` });
    }

    return (
        <div className="px-3 py-2 flex items-center gap-3">
            <div className="min-w-0 flex-1">
                <div className="text-[12px] text-slate-700 truncate">{mailbox.email}</div>
                {unverified && value && (
                    <div className="flex items-center gap-1 text-[11px] text-amber-600 mt-0.5">
                        <AlertTriangleIcon className="w-3 h-3 shrink-0" />
                        Not verified by Gmail anymore: this mailbox is held for this campaign.
                    </div>
                )}
            </div>
            <div className="w-72 shrink-0">
                {isGmail ? (
                    <SelectMenu
                        value={value}
                        onChange={onChange}
                        options={options}
                        fullWidth
                        disabled={identity.isLoading}
                        aria-label={`Send as for ${mailbox.email}`}
                    />
                ) : (
                    <div className="text-[11.5px] text-slate-400">Own address (no send-as on this provider)</div>
                )}
            </div>
        </div>
    );
}
