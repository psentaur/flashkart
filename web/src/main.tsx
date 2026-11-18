import { useMemo } from "react";
import { createRoot } from "react-dom/client";
import { BrowserRouter, Navigate, Route, Routes, useLocation, useNavigate } from "react-router-dom";
import type { ReactNode } from "react";
import { Admin, Diagnostics } from "./admin";
import { Analytics } from "./analytics";
import { Activity, Store, newIntents } from "./customer";
import { AccountControls, Authentication, Login, demoMode, useSession } from "./auth";
import { Team } from "./team";
import { message } from "./api";
import "./style.css";

const pages = [
  ["Storefront", "/", "public"], ["Activity", "/activity", "customer"],
  ["Analytics", "/analytics", "admin"], ["Administration", "/admin", "admin"],
  ["Replicas", "/replicas", "admin"], ["Team", "/team", "owner"],
] as const;

function Guard({ role = "customer", children }: { role?: "customer" | "admin" | "owner"; children: ReactNode }) {
  const auth = useSession();
  const location = useLocation();
  if (auth.loading) return <p role="status">Checking your session…</p>;
  if (auth.error !== undefined) return <p role="alert">{message(auth.error)}</p>;
  if (!auth.principal) return <Navigate to={"/login?returnTo=" + encodeURIComponent(location.pathname + location.search)} replace />;
  const allowed = role === "customer" || auth.principal.role === "owner" || (role === "admin" && auth.principal.role === "admin");
  if (!allowed) return <section><h1>Access restricted</h1><p>Your account does not have access to this page.</p></section>;
  return children;
}

function App() {
  const auth = useSession();
  const navigate = useNavigate();
  const location = useLocation();
  const intents = useMemo(newIntents, [auth.principal?.user_id]);
  const staff = auth.principal?.role === "admin" || auth.principal?.role === "owner";
  const visible = pages.filter(([, , role]) => role === "public" || (!auth.loading &&
    (role === "customer" ? !!auth.principal : role === "admin" ? staff : auth.principal?.role === "owner")));
  return <>
    <a className="skip" href="#main">Skip to content</a>
    <header className="header"><div className="shell navigation">
      <button className="brand" onClick={() => navigate("/")}><span aria-hidden="true">↯</span> Flashkart</button>
      <nav aria-label="Main navigation">{visible.map(([label, path]) => <button key={path}
        aria-current={location.pathname === path ? "page" : undefined} onClick={() => navigate(path)}>{label}</button>)}</nav>
      {auth.principal ? <AccountControls /> : <button className="secondary" onClick={() => navigate("/login")}>Sign in</button>}
      {demoMode && <button className="secondary" onClick={() => navigate("/login")}>Access tokens</button>}
    </div></header>
    <main className="shell main" id="main"><Routes>
      <Route path="/" element={<Store key={auth.principal?.user_id || "guest"} token={auth.customer} intents={intents} />} />
      <Route path="/login/*" element={<Login />} />
      <Route path="/sign-up/*" element={<Login signUp />} />
      <Route path="/activity" element={<Guard><Activity key={auth.principal?.user_id} token={auth.customer} /></Guard>} />
      <Route path="/analytics" element={<Guard role="admin"><Analytics token={auth.admin} /></Guard>} />
      <Route path="/admin" element={<Guard role="admin"><Admin token={auth.admin} /></Guard>} />
      <Route path="/replicas" element={<Guard role="admin"><Diagnostics token={auth.admin} /></Guard>} />
      <Route path="/team" element={<Guard role="owner"><Team token={auth.admin} /></Guard>} />
      <Route path="*" element={<Navigate to="/" replace />} />
    </Routes></main>
  </>;
}

const root = document.getElementById("root");
if (!root) throw new Error("Missing Flashkart root element");
createRoot(root).render(<BrowserRouter><Authentication><App /></Authentication></BrowserRouter>);
