import { useEffect, useRef, useState } from "react";
import { ApiError, api, date, message, money, segment, statusOf } from "./api";
import type { Attempt, Notification, Product, Sale, Stock } from "./api";
import { submitAttempt } from "./checkout";
import { useQuery } from "./hooks";
import type { PaymentChoice, Purchase } from "./session";
import { saleByID } from "./session";
import { Feedback, Pipeline, ProductImage, StockBar, useNow } from "./ui";
import type { Step } from "./ui";

function stored<T>(key: string, fallback: T): T {
  try { const v = sessionStorage.getItem("flashkart." + key); return v ? JSON.parse(v) as T : fallback; } catch { return fallback; }
}
function store(key: string, value: unknown) {
  try { sessionStorage.setItem("flashkart." + key, JSON.stringify(value)); } catch { /* memory only */ }
}

/**
 * Administrator tool: a crowd of shoppers hits one live sale at once, and the run is traced through
 * every service on the purchase path. Shoppers act as customers, so the run uses the customer key.
 */
export function Rush({ token }: { token: string }) {
  const products = useQuery<Product[]>("/api/products");
  const sales = useQuery<Sale[]>("/api/sales", undefined, 10_000);
  const stock = useQuery<Stock[]>("/api/inventory/sales", undefined, 2000);
  const now = useNow(5000);
  const [key, setKey] = useState(() => stored("rush-key", ""));
  const [runs, setRuns] = useState<Purchase[]>(() => stored("rush-runs", []));
  const [saleID, setSaleID] = useState("");
  const [count, setCount] = useState(150);
  const [payment, setPayment] = useState<PaymentChoice>("card");
  const [busy, setBusy] = useState(false);
  const running = useRef(false);
  useEffect(() => store("rush-runs", runs), [runs]);
  const notifications = useQuery<Notification[]>(key && runs.length ? "/api/notifications" : null, key, 2000);
  const catalog = new Map(products.data?.map(item => [item.product_id, item]));
  const bySale = saleByID(sales.data);
  const levels = new Map(stock.data?.map(item => [item.sale_id, item]));
  const live = (sales.data || []).filter(s => statusOf(s, levels.get(s.sale_id), now) === "Live");
  const sale = bySale.get(saleID) || live[0];
  const delivered = new Map(notifications.data?.map(item => [item.attempt.attempt_id, item.attempt]));

  async function run() {
    if (running.current || !sale || !key) return;
    running.current = true;
    setBusy(true);
    store("rush-key", key);
    const crowd = new Date().toISOString();
    // Every shopper at once, without 429 retries, so admission control stays visible.
    const results = await Promise.all(Array.from({ length: count }, () => submitAttempt(key, sale, payment, 0)));
    running.current = false;
    setBusy(false);
    setRuns(previous => [...results.map(r => ({ ...r, crowd })), ...previous]);
  }

  const crowds = [...new Set(runs.map(r => r.crowd!))];
  return <section className="stack">
    <div className="section-head"><div><h1>Rush test</h1>
      <p>Simulate a crowd of shoppers hitting a live sale at the same moment, and see how the services handle them.</p></div></div>
    <div className="panel">
      <Feedback query={sales} />
      {live.length === 0 ? <p className="empty">No sale is live. Publish a sale whose window includes now, then come back.</p>
        : <div className="form-grid">
          <label className="full">Live sale<select value={sale?.sale_id} onChange={e => setSaleID(e.target.value)}>
            {live.map(s => <option key={s.sale_id} value={s.sale_id}>{catalog.get(s.terms.product_id)?.name || s.terms.product_id} · {money(s.terms)}</option>)}</select></label>
          {sale && <div className="full chosen"><ProductImage product={catalog.get(sale.terms.product_id)} small />
            <div style={{ flex: 1 }}><StockBar stock={levels.get(sale.sale_id)} limit={sale.terms.stock} detail /></div></div>}
        </div>}
    </div>
    {sale && <CloudBurst token={token} sale={sale} name={catalog.get(sale.terms.product_id)?.name || sale.terms.product_id} />}
    {sale && <div className="panel">
      <h2>Browser shoppers</h2>
      <p className="small">Shoppers sent from this browser go through the whole purchase path, shown service by service below.
        A browser sends only a few requests at a time, so they are never throttled — use the cloud burst for that.</p>
      <div className="form-grid">
          <label>Shoppers at once<input type="number" min={1} max={300} value={count}
            onChange={e => setCount(Math.min(300, Math.max(1, e.target.valueAsNumber || 1)))} /></label>
          <label>Their payment<select value={payment} onChange={e => setPayment(e.target.value as PaymentChoice)}>
            <option value="card">Card goes through</option><option value="decline">Card is declined</option></select></label>
          <label className="full">Customer access key<input type="password" value={key} onChange={e => setKey(e.target.value)} autoComplete="off" />
            <span className="small">The crowd shops as customers, so it signs requests with the customer key.</span></label>
          <div className="full"><button className="flash-button" disabled={busy || !key} onClick={() => void run()}>
            {busy ? "Sending shoppers…" : `Send ${count} shopper${count === 1 ? "" : "s"}`}</button></div>
      </div>
    </div>}
    {crowds.map(crowd => {
      const members = runs.filter(r => r.crowd === crowd);
      return <CrowdCard key={crowd} members={members} sale={bySale.get(members[0].sale_id)}
        product={catalog.get(members[0].product_id)} delivered={delivered} />;
    })}
    {runs.length > 0 && <div><button className="secondary" onClick={() => setRuns([])}>Clear results</button></div>}
  </section>;
}

/** One rush: how the shoppers were spread across the services. */
function CrowdCard({ members, sale, product, delivered }: {
  members: Purchase[]; sale?: Sale; product?: Product; delivered: Map<string, Attempt>;
}) {
  const admitted = members.filter(m => m.admission === "admitted");
  const throttled = members.length - admitted.length;
  const outcomes = admitted.map(m => delivered.get(m.attempt_id));
  const count = (test: (a?: Attempt) => boolean) => outcomes.filter(test).length;
  const confirmed = count(a => a?.state === "CONFIRMED");
  const declined = count(a => a?.state === "CANCELLED");
  const soldOut = count(a => a?.state === "REJECTED");
  const reserved = confirmed + declined;
  const pending = admitted.length - confirmed - declined - soldOut;
  const steps: Step[] = [
    { service: "Admission Controller", label: `${admitted.length} let in · ${throttled} turned away (overload)`, state: throttled ? "failed" : "done" },
    { service: "Inventory Reservation", label: `${reserved} got a unit · ${soldOut} sold out`, state: pending ? "active" : "done" },
    { service: "Order Service", label: `${confirmed} confirmed · ${declined} cancelled`, state: pending ? "active" : "done" },
    { service: "Payment Service", label: `${confirmed} paid · ${declined} declined`, state: pending ? "active" : declined ? "failed" : "done" },
    { service: "Notification Service", label: `${admitted.length - pending} of ${admitted.length} shoppers told the result`, state: pending ? "active" : "done" },
  ];
  return <article className="panel order crowd-card">
    <div className="order-head">
      <ProductImage product={product} small />
      <div><h2>{members.length} shopper{members.length === 1 ? "" : "s"} · {product?.name || members[0].product_id}</h2>
        <p className="small">{sale ? money(sale.terms) : ""} · {date(members[0].created_at)}{members[0].payment === "decline" && " · cards declined"}</p></div>
      <span className={`pill ${pending ? "soon" : "live"}`}>{pending ? `${pending} in progress` : "Complete"}</span>
    </div>
    <Pipeline steps={steps} />
    <p className="small">Stock limit {sale?.terms.stock ?? "—"}: however many shoppers arrive, no more units are sold than exist.</p>
  </article>;
}

type Burst = { burst_id: string; sale_id: string; shoppers: number; seconds: number; state: "RUNNING" | "COMPLETED";
  sent: number; admitted: number; throttled: number; failed: number };

/**
 * A real flash crowd, sent from inside the cluster by the Flash Sale Manager: hundreds of shoppers in
 * parallel for up to 20 seconds. Unlike browser shoppers, this is enough to trip admission control.
 */
function CloudBurst({ token, sale, name }: { token: string; sale: Sale; name: string }) {
  const [burst, setBurst] = useState<Burst>();
  const [error, setError] = useState<unknown>();
  const [starting, setStarting] = useState(false);
  const running = burst?.state === "RUNNING";
  useEffect(() => {
    if (!burst || burst.state !== "RUNNING") return;
    const timer = window.setInterval(async () => {
      try { setBurst(await api<Burst>("/api/admin/load-bursts/" + segment(burst.burst_id), { token })); }
      catch (err) { setError(err); }
    }, 1000);
    return () => window.clearInterval(timer);
  }, [burst, token]);

  async function start() {
    if (starting || running) return;
    setStarting(true);
    setError(undefined);
    try {
      setBurst(await api<Burst>("/api/admin/load-bursts", { method: "POST", token, body: { sale_id: sale.sale_id, shoppers: 400, seconds: 20 } }));
    } catch (err) {
      setError(err instanceof ApiError && err.code === "BURST_RUNNING" ? new Error("A burst is already running. Try again in a few seconds.") : err);
    } finally { setStarting(false); }
  }
  const share = (n: number) => burst && burst.sent ? Math.round(n / burst.sent * 100) : 0;
  return <div className="panel burst">
    <div className="section-head"><div><h2>⚡ Cloud burst</h2>
      <p className="small">400 shoppers order <strong>{name}</strong> in parallel for 20 seconds, sent from inside the cloud to the Admission Controller.
        This is a real flash crowd: watch it turn shoppers away when they arrive faster than it can admit them.</p></div>
      <button className="flash-button" disabled={starting || running} onClick={() => void start()}>
        {running ? "Burst running…" : starting ? "Starting…" : "Run 20-second burst"}</button></div>
    {error !== undefined && <p role="alert" className="error">{message(error)}</p>}
    {burst && <>
      <div className="tiles">
        <div className="tile"><span>Orders sent</span><strong>{burst.sent.toLocaleString()}</strong><small>{running ? "Still sending…" : "Finished"}</small></div>
        <div className="tile good"><span>Admitted</span><strong>{burst.admitted.toLocaleString()}</strong><small>{share(burst.admitted)}% let in to buy</small></div>
        <div className="tile warn"><span>Throttled</span><strong>{burst.throttled.toLocaleString()}</strong><small>{share(burst.throttled)}% turned away: too many at once</small></div>
        <div className="tile"><span>Errors</span><strong>{burst.failed.toLocaleString()}</strong><small>Timeouts or unexpected answers</small></div>
      </div>
      {!running && <p className="small">Admitted orders then compete for the {sale.terms.stock} units in stock: Inventory sells exactly that many and
        rejects the rest as sold out. Open <strong>Analytics</strong> in about a minute to see throttled and sold-out counts for this sale.</p>}
    </>}
  </div>;
}
