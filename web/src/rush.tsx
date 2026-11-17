import { useEffect, useRef, useState } from "react";
import { date, money, statusOf } from "./api";
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
export function Rush() {
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
      <p>Simulate a crowd of shoppers hitting a live flash sale at the same moment, and see how each service handled them.</p></div></div>
    <div className="panel">
      <Feedback query={sales} />
      {live.length === 0 ? <p className="empty">No sale is live. Publish a sale whose window includes now, then come back.</p>
        : <div className="form-grid">
          <label className="full">Live sale<select value={sale?.sale_id} onChange={e => setSaleID(e.target.value)}>
            {live.map(s => <option key={s.sale_id} value={s.sale_id}>{catalog.get(s.terms.product_id)?.name || s.terms.product_id} · {money(s.terms)}</option>)}</select></label>
          {sale && <div className="full chosen"><ProductImage product={catalog.get(sale.terms.product_id)} small />
            <div style={{ flex: 1 }}><StockBar stock={levels.get(sale.sale_id)} limit={sale.terms.stock} detail /></div></div>}
          <label>Shoppers at once<input type="number" min={1} max={300} value={count}
            onChange={e => setCount(Math.min(300, Math.max(1, e.target.valueAsNumber || 1)))} /></label>
          <label>Their payment<select value={payment} onChange={e => setPayment(e.target.value as PaymentChoice)}>
            <option value="card">Card goes through</option><option value="decline">Card is declined</option></select></label>
          <label className="full">Customer access key<input type="password" value={key} onChange={e => setKey(e.target.value)} autoComplete="off" />
            <span className="small">The crowd shops as customers, so it signs requests with the customer key.</span></label>
          <div className="full"><button className="flash-button" disabled={busy || !key} onClick={() => void run()}>
            {busy ? "Sending shoppers…" : `Send ${count} shopper${count === 1 ? "" : "s"}`}</button></div>
        </div>}
    </div>
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
