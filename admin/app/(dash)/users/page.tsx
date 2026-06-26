"use client";

import { useState } from "react";
import { useConfirm } from "@/components/confirm";
import { api } from "@/lib/api";
import { useFetch } from "@/lib/useFetch";
import { Badge, Empty, ErrorBanner, PageHeader, Spinner } from "@/components/ui";

type User = { id: number; email: string; is_active: boolean; created_at: string; last_login_at: string | null };

export default function UsersPage() {
  const { data, error, loading, refresh } = useFetch<{ users: User[] }>("users");
  const [actionError, setActionError] = useState<string | null>(null);

  const users = data?.users ?? [];

  async function act(fn: () => Promise<any>) {
    setActionError(null);
    try {
      await fn();
      await refresh();
    } catch (e: any) {
      setActionError(e.message || "Action failed");
    }
  }

  return (
    <div>
      <PageHeader title="Users" subtitle="Admin panel accounts. Anyone listed can sign in and manage the gateway." />
      <ErrorBanner message={actionError || error} />

      <div className="max-w-3xl space-y-6">
        <CreateUser onCreate={(email, password) => act(() => api("users", { method: "POST", body: JSON.stringify({ email, password }) }))} />

        {loading ? (
          <Spinner />
        ) : users.length === 0 ? (
          <Empty>No users yet.</Empty>
        ) : (
          <div className="card overflow-x-auto p-0">
            <table className="min-w-full divide-y divide-edge">
              <thead>
                <tr>
                  <th className="th">Email</th>
                  <th className="th">Status</th>
                  <th className="th">Last login</th>
                  <th className="th text-right">Actions</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-edge">
                {users.map((u) => (
                  <UserRow
                    key={u.id}
                    user={u}
                    onToggle={() => act(() => api(`users/${u.id}`, { method: "PUT", body: JSON.stringify({ is_active: !u.is_active }) }))}
                    onReset={(pw) => act(() => api(`users/${u.id}`, { method: "PUT", body: JSON.stringify({ password: pw }) }))}
                    onDelete={() => act(() => api(`users/${u.id}`, { method: "DELETE" }))}
                  />
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>
    </div>
  );
}

function UserRow({
  user,
  onToggle,
  onReset,
  onDelete,
}: {
  user: User;
  onToggle: () => Promise<void>;
  onReset: (pw: string) => Promise<void>;
  onDelete: () => Promise<void>;
}) {
  const [resetting, setResetting] = useState(false);
  const [pw, setPw] = useState("");
  const confirm = useConfirm();

  return (
    <tr>
      <td className="td font-mono">{user.email}</td>
      <td className="td">
        <Badge tone={user.is_active ? "green" : "slate"}>{user.is_active ? "Active" : "Disabled"}</Badge>
      </td>
      <td className="td text-slate-400">{user.last_login_at ? new Date(user.last_login_at).toLocaleString() : "never"}</td>
      <td className="td">
        {resetting ? (
          <div className="flex items-center justify-end gap-2">
            <input
              className="input w-44"
              type="password"
              value={pw}
              onChange={(e) => setPw(e.target.value)}
              placeholder="new password (min 8)"
              autoFocus
            />
            <button
              className="btn-primary"
              disabled={pw.length < 8}
              onClick={async () => {
                await onReset(pw);
                setPw("");
                setResetting(false);
              }}
            >
              Save
            </button>
            <button className="btn-ghost" onClick={() => { setPw(""); setResetting(false); }}>
              Cancel
            </button>
          </div>
        ) : (
          <div className="flex items-center justify-end gap-2">
            <button className="btn-ghost" onClick={onToggle}>
              {user.is_active ? "Disable" : "Enable"}
            </button>
            <button className="btn-ghost" onClick={() => setResetting(true)}>
              Reset password
            </button>
            <button
              className="btn-danger"
              onClick={async () => {
                if (
                  await confirm({
                    title: "Delete user?",
                    body: `${user.email} will lose access immediately.`,
                    confirmLabel: "Delete",
                  })
                )
                  onDelete();
              }}
            >
              Delete
            </button>
          </div>
        )}
      </td>
    </tr>
  );
}

function CreateUser({ onCreate }: { onCreate: (email: string, password: string) => Promise<void> }) {
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [busy, setBusy] = useState(false);

  const valid = /.+@.+\..+/.test(email) && password.length >= 8;

  return (
    <div className="card space-y-3">
      <h2 className="text-sm font-semibold text-slate-300">Create user</h2>
      <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
        <div>
          <label className="text-xs text-slate-400">Email</label>
          <input className="input mt-1" type="email" autoComplete="off" value={email} onChange={(e) => setEmail(e.target.value)} placeholder="person@example.com" />
        </div>
        <div>
          <label className="text-xs text-slate-400">Password (min 8 chars)</label>
          <input className="input mt-1" type="password" autoComplete="new-password" value={password} onChange={(e) => setPassword(e.target.value)} placeholder="••••••••" />
        </div>
      </div>
      <div className="flex justify-end">
        <button
          className="btn-primary"
          disabled={!valid || busy}
          onClick={async () => {
            setBusy(true);
            await onCreate(email.trim(), password);
            setBusy(false);
            setEmail("");
            setPassword("");
          }}
        >
          {busy ? "Creating…" : "Create user"}
        </button>
      </div>
    </div>
  );
}
