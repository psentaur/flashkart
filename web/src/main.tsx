import { useState } from "react";
import { createRoot } from "react-dom/client";
import { Admin, Diagnostics } from "./admin";
import { Analytics } from "./analytics";
import { Activity, Store, newIntents } from "./customer";
import "./style.css";

const pages = ["Storefront", "Activity", "Analytics", "Administration", "Replicas"] as const;

function App() {
  const [page, setPage] = useState<(typeof pages)[number]>("Storefront");
  const [customer, setCustomer] = useState("");
  const [intents, setIntents] = useState(newIntents);
  const [admin, setAdmin] = useState("");
  const [settings, setSettings] = useState(false);
  return <>
    <a className="skip" href="#main">Skip to content</a>
    <header className="header"><div className="shell navigation">
      <button className="brand" onClick={() => setPage("Storefront")}><span aria-hidden="true">↯</span> Flashkart</button>
      <nav aria-label="Main navigation">{pages.map(item => <button key={item}
        aria-current={page === item ? "page" : undefined} onClick={() => setPage(item)}>{item}</button>)}</nav>
      <button className="secondary" aria-expanded={settings} onClick={() => setSettings(value => !value)}>Access tokens</button>
    </div></header>
    <main className="shell main" id="main">
      {settings && <section className="panel tokens" aria-label="Access tokens">
        <label>Customer token<input type="password" autoComplete="off" value={customer} onChange={e => { setCustomer(e.target.value); setIntents(newIntents()); }} /></label>
        <label>Administrator token<input type="password" autoComplete="off" value={admin} onChange={e => setAdmin(e.target.value)} /></label>
      </section>}
      {page === "Storefront" && <Store key={customer} token={customer.trim()} intents={intents} />}
      {page === "Activity" && <Activity token={customer.trim()} />}
      {page === "Analytics" && <Analytics />}
      {page === "Administration" && <Admin key={admin} token={admin.trim()} />}
      {page === "Replicas" && <Diagnostics key={admin} token={admin.trim()} />}
    </main>
  </>;
}

const root = document.getElementById("root");
if (!root) throw new Error("Missing Flashkart root element");
createRoot(root).render(<App />);
