"use client";

import { useState } from "react";
import { api } from "@/lib/api";
import { refreshEventCodes } from "@/lib/useEventCodes";

// AddEventCode adds a code to the standard_event_codes picklist (POST /api/event-codes)
// so it becomes selectable in every mapping's Event code dropdown. Collapsed by
// default to keep the mapping table the focus.
export function AddEventCode() {
  const [open, setOpen] = useState(false);
  const [code, setCode] = useState("");
  const [category, setCategory] = useState("");
  const [notes, setNotes] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);

  const valid = code.trim().length > 0;

  async function save() {
    setBusy(true);
    setError(null);
    setNotice(null);
    try {
      await api("event-codes", {
        method: "POST",
        body: JSON.stringify({ code: code.trim(), category: category.trim(), notes: notes.trim() }),
      });
      refreshEventCodes(); // make it appear in the dropdowns immediately
      setNotice(`Added "${code.trim()}" — it's now selectable in the Event code dropdown.`);
      setCode("");
      setCategory("");
      setNotes("");
    } catch (e: any) {
      setError(e.message || "Failed to add event code");
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="card mb-6">
      <button type="button" className="flex w-full items-center justify-between text-left" onClick={() => setOpen((o) => !o)}>
        <h2 className="text-sm font-semibold text-slate-300">Add a new event code</h2>
        <span className="text-slate-400">{open ? "−" : "+"}</span>
      </button>

      {open && (
        <div className="mt-3 space-y-3">
          <p className="text-xs text-slate-400">
            Adds a code to the standard list so it can be selected from the Event code dropdown in any mapping.
          </p>
          {error && <div className="rounded-md border border-rose-500/40 bg-rose-500/10 px-3 py-2 text-sm text-rose-200">{error}</div>}
          {notice && (
            <div className="rounded-md border border-emerald-500/40 bg-emerald-500/10 px-3 py-2 text-sm text-emerald-200">{notice}</div>
          )}
          <div className="grid grid-cols-1 gap-3 md:grid-cols-12">
            <div className="md:col-span-5">
              <label className="text-xs text-slate-400">Event code</label>
              <input className="input mt-1" value={code} onChange={(e) => setCode(e.target.value)} placeholder="e.g. COLLISION:SEVERE" />
            </div>
            <div className="md:col-span-3">
              <label className="text-xs text-slate-400">Category</label>
              <input className="input mt-1" value={category} onChange={(e) => setCategory(e.target.value)} placeholder="optional" />
            </div>
            <div className="md:col-span-4">
              <label className="text-xs text-slate-400">Notes</label>
              <input className="input mt-1" value={notes} onChange={(e) => setNotes(e.target.value)} placeholder="optional" />
            </div>
          </div>
          <div className="flex justify-end">
            <button className="btn-primary" disabled={!valid || busy} onClick={save}>
              {busy ? "Saving…" : "Add event code"}
            </button>
          </div>
        </div>
      )}
    </div>
  );
}
