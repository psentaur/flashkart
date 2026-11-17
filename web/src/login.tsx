import { useState } from "react";
import type { FormEvent } from "react";
import { ApiError, api } from "./api";
import type { Role, Session } from "./session";

const roles: { role: Role; title: string; text: string; probe: string }[] = [
  { role: "customer", title: "Customer", text: "Browse live flash sales, fill your cart, check out and track every order.", probe: "/api/notifications" },
  { role: "admin", title: "Administrator", text: "Manage products and images, schedule sales, watch analytics, replicas and services.", probe: "/api/admin/replicas" },
];

export function Login({ onLogin }: { onLogin: (session: Session) => void }) {
  const [role, setRole] = useState<Role>("customer");
  const [token, setToken] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const selected = roles.find(item => item.role === role)!;

  async function submit(e: FormEvent) {
    e.preventDefault();
    const value = token.trim();
    if (!value || busy) return;
    setBusy(true);
    setError("");
    try {
      // The access key is the bearer token; the probe confirms it belongs to the chosen role.
      await api(selected.probe, { token: value });
      onLogin({ role, token: value });
    } catch (err) {
      setError(err instanceof ApiError && (err.status === 401 || err.status === 403)
        ? `That access key is not valid for the ${selected.title.toLowerCase()} role.`
        : "Could not reach Flashkart. Check that the services are running and try again.");
    } finally { setBusy(false); }
  }

  return <div className="login">
    <div className="login-intro">
      <span className="brand-mark" aria-hidden="true">↯</span>
      <h1>Flashkart</h1>
      <p>Flash sales for limited-stock products, built from eight cloud microservices.</p>
    </div>
    <form className="panel login-card" onSubmit={submit} aria-label="Sign in">
      <h2>Sign in</h2>
      <div className="role-picker" role="radiogroup" aria-label="Role">
        {roles.map(item => <button type="button" key={item.role} role="radio" aria-checked={role === item.role}
          className={role === item.role ? "role selected" : "role"} onClick={() => { setRole(item.role); setError(""); }}>
          <strong>{item.title}</strong><span>{item.text}</span>
        </button>)}
      </div>
      <label>{selected.title} access key
        <input type="password" autoComplete="current-password" required value={token} onChange={e => setToken(e.target.value)} />
      </label>
      {error && <p className="error" role="alert">{error}</p>}
      <button disabled={busy || !token.trim()}>{busy ? "Signing in…" : `Sign in as ${selected.title.toLowerCase()}`}</button>
    </form>
  </div>;
}
