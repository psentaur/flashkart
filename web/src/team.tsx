import { useState } from "react";
import type { FormEvent } from "react";
import { api, message, segment } from "./api";
import type { Credential } from "./api";
import { useQuery } from "./hooks";
import { Feedback } from "./ui";

type User = { id: string; email: string; role: string; verified: boolean; disabled: boolean };
type Invitation = { id: string; email: string; status: string; expires_at?: number };
type Page<T> = { data: T[]; total_count: number };
const size = 20;

export function Team({ token }: { token: Credential }) {
  const [email, setEmail] = useState("");
  const [search, setSearch] = useState("");
  const [invitationEmail, setInvitationEmail] = useState("");
  const [offset, setOffset] = useState(0);
  const [inviteOffset, setInviteOffset] = useState(0);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>();
  const [notice, setNotice] = useState("");
  const [confirm, setConfirm] = useState<User>();
  const users = useQuery<Page<User>>(`/api/admin/team/users?email=${segment(search)}&offset=${offset}&limit=${size}`, token);
  const invites = useQuery<Page<Invitation>>(`/api/admin/team/invitations?offset=${inviteOffset}&limit=${size}`, token);

  async function change(path: string, method: string, body?: unknown) {
    setBusy(true); setError(undefined); setNotice("");
    try {
      await api(path, { method, token, body });
      users.reload(); invites.reload(); setConfirm(undefined);
      setNotice("Team updated. Changes to existing sessions apply when their short-lived token refreshes.");
      return true;
    } catch (error) { setError(error); return false; }
    finally { setBusy(false); }
  }
  async function invite(e: FormEvent) {
    e.preventDefault();
    if (await change("/api/admin/team/invitations", "POST", { email: invitationEmail.trim() })) setInvitationEmail("");
  }
  return <section className="stack"><div><h1>Team</h1><p>Invite trusted people to manage your storefront. Only the owner can change admin access.</p></div>
    {notice && <p role="status">{notice}</p>}
    {error !== undefined && <p role="alert" className="error">{message(error)}</p>}
    <div className="panel"><h2>Invite an admin</h2><form className="inline-form" onSubmit={invite}>
      <label>Email address<input type="email" required maxLength={254} value={invitationEmail} onChange={e => setInvitationEmail(e.target.value)} /></label>
      <button disabled={busy}>Send invitation</button>
    </form><p>Invitations expire after seven days. For someone with an existing account, grant access below.</p></div>
    <div className="panel table-wrap"><h2>Users</h2><form className="inline-form" onSubmit={e => { e.preventDefault(); setOffset(0); setSearch(email.trim()); }}>
      <label>Find by email<input type="search" value={email} onChange={e => setEmail(e.target.value)} /></label><button className="secondary">Search</button>
    </form><Feedback query={users} />
    <table><thead><tr><th>Email</th><th>Role</th><th>Account</th><th>Access</th></tr></thead><tbody>
      {users.data?.data.map(u => <tr key={u.id}><td>{u.email || u.id}</td><td><span className="pill">{u.role}</span></td>
        <td>{u.disabled ? "Disabled" : u.verified ? "Verified" : "Email unverified"}</td><td>{u.role === "owner" ? "Protected owner" :
          <button className="secondary" disabled={busy || (u.role !== "admin" && (!u.verified || u.disabled))} onClick={() => setConfirm(u)}>
            {u.role === "admin" ? "Remove admin access" : "Grant admin access"}</button>}</td></tr>)}
    </tbody></table>{users.data?.data.length === 0 && <p>No matching users.</p>}
    <Pagination offset={offset} total={users.data?.total_count || 0} onChange={setOffset} />
    </div>
    {confirm && <section className="panel confirmation" role="alertdialog" aria-label="Confirm access change" aria-describedby="access-change-description">
      <h2>{confirm.role === "admin" ? "Remove admin access?" : "Grant admin access?"}</h2>
      <p id="access-change-description">{confirm.email} {confirm.role === "admin" ? "will keep their customer account and purchases." : "will be able to manage products, sales, analytics, and replicas."}</p>
      <div className="catalog-list"><button disabled={busy} onClick={() => void change(`/api/admin/team/users/${segment(confirm.id)}`, "PATCH", { role: confirm.role === "admin" ? "customer" : "admin" })}>Confirm access change</button>
        <button className="secondary" disabled={busy} onClick={() => setConfirm(undefined)}>Cancel</button></div>
    </section>}
    <div className="panel table-wrap"><h2>Invitations</h2><Feedback query={invites} />
      <table><thead><tr><th>Email</th><th>Status</th><th>Expires</th><th>Action</th></tr></thead><tbody>{invites.data?.data.map(i =>
        <tr key={i.id}><td>{i.email}</td><td>{i.status}</td><td>{i.expires_at ? new Date(i.expires_at).toLocaleDateString() : "—"}</td>
          <td>{i.status === "pending" && <button className="secondary" disabled={busy} onClick={() => void change(`/api/admin/team/invitations/${segment(i.id)}`, "DELETE")}>Revoke invitation</button>}</td></tr>)}</tbody></table>
      {invites.data?.data.length === 0 && <p>No invitations yet.</p>}
      <Pagination offset={inviteOffset} total={invites.data?.total_count || 0} onChange={setInviteOffset} />
    </div>
  </section>;
}

function Pagination({ offset, total, onChange }: { offset: number; total: number; onChange: (offset: number) => void }) {
  return <div className="pagination"><button className="secondary" disabled={!offset} onClick={() => onChange(Math.max(0, offset - size))}>Previous</button>
    <span>{total ? `${offset + 1}–${Math.min(total, offset + size)} of ${total}` : "0 results"}</span>
    <button className="secondary" disabled={offset + size >= total} onClick={() => onChange(offset + size)}>Next</button></div>;
}
