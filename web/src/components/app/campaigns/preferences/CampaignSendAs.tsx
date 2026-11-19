// Which Gmail send-as addresses this campaign rotates between on each of its
// individually picked mailboxes. Each new lead gets one at random and keeps it.

import React from "react";
import { AlertTriangleIcon } from "lucide-react";
import useEmails from "@/lib/api/hooks/app/emails/useEmails";
import useSendIdentity from "@/lib/api/hooks/app/emails/useSendIdentity";
import { Checkbox } from "@/components/ui/checkbox";
import type Inbox from "@/lib/api/models/app/emails/Inbox";

export default function CampaignSendAs({
    accountIds,
    sendAs,
    onChange,
    unverified,
}: {
    accountIds: string[];
    sendAs: Record<string, string[]>;
    onChange: (accountId: string, aliases: string[]) => void;
    /** Saved aliases the provider no longer verifies, by mailbox id. */
    unverified: Record<string, string[]>;
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
                        value={sendAs[mailbox.id] ?? []}
                        onChange={(v) => onChange(mailbox.id, v)}
                        unverified={unverified[mailbox.id] ?? []}
                    />
                ))}
            </div>
            <p className="text-[11px] text-slate-400 mt-1.5">
                Gmail mailboxes only. Tick one or more verified addresses: each new lead gets one at random and keeps it
                for every follow-up. None ticked sends from the mailbox&apos;s own address. If Gmail stops verifying an
                address, new leads skip it and the leads already on it wait until it is verified again.
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
    value: string[];
    onChange: (v: string[]) => void;
    unverified: string[];
}) {
    const isGmail = mailbox.provider === "gmail";
    const identity = useSendIdentity(mailbox.id, isGmail);
    const lower = (s: string) => s.toLowerCase();
    const selected = new Set(value.map(lower));
    const revoked = new Set(unverified.map(lower));

    type Option = { email: string; label: string; verified: boolean };
    const options: Option[] = (identity.data?.identities ?? []).map((id) => ({
        email: id.email,
        label: id.name ? `${id.name} · ${id.email}` : id.email,
        verified: id.verified,
    }));
    // A stored choice the provider no longer lists still has to show as ticked.
    for (const v of value) {
        if (!options.some((o) => lower(o.email) === lower(v))) {
            options.push({ email: v, label: `${v} (no longer listed)`, verified: false });
        }
    }

    const toggle = (email: string) => {
        if (selected.has(lower(email))) onChange(value.filter((v) => lower(v) !== lower(email)));
        else onChange([...value, email]);
    };

    return (
        <div className="px-3 py-2">
            <div className="text-[12px] text-slate-700 truncate">{mailbox.email}</div>
            {!isGmail ? (
                <div className="text-[11.5px] text-slate-400 mt-0.5">Own address (no send-as on this provider)</div>
            ) : identity.isLoading ? (
                <div className="text-[11.5px] text-slate-400 mt-0.5">Loading addresses…</div>
            ) : options.length === 0 ? (
                <div className="text-[11.5px] text-slate-400 mt-0.5">No send-as address verified in Gmail yet</div>
            ) : (
                <div className="mt-1.5 flex flex-col gap-1">
                    {options.map((o) => {
                        const checked = selected.has(lower(o.email));
                        const stale = checked && (revoked.has(lower(o.email)) || !o.verified);
                        return (
                            <label
                                key={o.email}
                                className={`flex items-center gap-2 text-[12px] ${
                                    o.verified || checked ? "text-slate-700 cursor-pointer" : "text-slate-400"
                                }`}
                            >
                                <Checkbox
                                    tone="slate"
                                    checked={checked}
                                    disabled={!o.verified && !checked}
                                    onChange={() => toggle(o.email)}
                                    aria-label={`Send as ${o.email} from ${mailbox.email}`}
                                />
                                <span className="truncate">{o.label}</span>
                                {!o.verified && !checked && <span className="text-[11px]">(not verified)</span>}
                                {stale && (
                                    <span className="inline-flex items-center gap-1 text-[11px] text-amber-600">
                                        <AlertTriangleIcon className="w-3 h-3 shrink-0" />
                                        not verified anymore: its leads wait
                                    </span>
                                )}
                            </label>
                        );
                    })}
                    {value.length === 0 && (
                        <div className="text-[11px] text-slate-400">
                            None ticked: sends as {mailbox.send_as_email?.trim() || mailbox.email}
                        </div>
                    )}
                </div>
            )}
        </div>
    );
}
