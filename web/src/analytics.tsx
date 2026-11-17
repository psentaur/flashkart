import { useEffect, useState } from "react";
import { api, date, formatMoney, isFlash, money, statusOf, segment, time } from "./api";
import type { Product, Sale, Stock, Window } from "./api";
import { useQuery } from "./hooks";
import { Feedback, ProductImage } from "./ui";

// Each window splits demand into outcomes; the colours match the legend.
const parts = [
  ["confirmed", "Confirmed purchases", "c-confirmed"],
  ["cancelled", "Reserved, then cancelled", "c-cancelled"],
  ["stock", "Rejected: sold out", "c-stock"],
  ["admission", "Rejected: throttled", "c-admission"],
  ["pending", "Still in progress", "c-pending"],
] as const;

function split(w: Window) {
  const cancelled = Math.max(0, w.reservations - w.confirmed_purchases);
  const known = w.confirmed_purchases + cancelled + w.stock_rejections + w.admission_rejections;
  return { confirmed: w.confirmed_purchases, cancelled, stock: w.stock_rejections, admission: w.admission_rejections, pending: Math.max(0, w.demand - known) };
}

export function Analytics() {
  const sales = useQuery<Sale[]>("/api/sales");
  const products = useQuery<Product[]>("/api/products");
  const stock = useQuery<Stock[]>("/api/inventory/sales", undefined, 5000);
  const levels = new Map(stock.data?.map(item => [item.sale_id, item]));
  const ordered = [...(sales.data || [])].sort((a, b) => b.terms.starts_at.localeCompare(a.terms.starts_at));
  const [chosen, setChosen] = useState("");
  const selection = chosen || ordered.find(sale => statusOf(sale, levels.get(sale.sale_id)) === "Live")?.sale_id || ordered[0]?.sale_id || "";
  const windows = useQuery<Window[]>(selection ? "/api/analytics/sales/" + segment(selection) : null, undefined, 5000);
  const catalog = new Map(products.data?.map(item => [item.product_id, item]));
  const sale = ordered.find(item => item.sale_id === selection);
  const product = sale && catalog.get(sale.terms.product_id);
  const level = stock.data?.find(item => item.sale_id === selection);
  const data = windows.data || [];
  const totals = data.reduce((sum, w) => ({
    demand: sum.demand + w.demand, admission: sum.admission + w.admission_rejections, stock: sum.stock + w.stock_rejections,
    reservations: sum.reservations + w.reservations, confirmed: sum.confirmed + w.confirmed_purchases,
    latency: sum.latency + (w.average_latency_ms ?? 0) * w.confirmed_purchases,
  }), { demand: 0, admission: 0, stock: 0, reservations: 0, confirmed: 0, latency: 0 });
  const latency = totals.confirmed ? Math.round(totals.latency / totals.confirmed) : undefined;
  const recent = data.slice(-15);
  const peak = Math.max(1, ...recent.map(w => w.demand));
  const label = (s: Sale) => `${catalog.get(s.terms.product_id)?.name || s.terms.product_id} · ${date(s.terms.starts_at)} (${statusOf(s, levels.get(s.sale_id))})`;

  return <section className="stack">
    <div><h1>Sales analytics</h1><p>Flink aggregates the purchase event stream into one-minute windows. A window appears about a minute after it closes.</p></div>
    <Feedback query={sales} />
    <Overview sales={ordered} catalog={new Map(products.data?.map(item => [item.product_id, item]))} onPick={setChosen} />
    <h2 className="spaced">One sale in detail</h2>
    <label className="sale-picker">Sale<select value={selection} onChange={e => setChosen(e.target.value)}>
      {ordered.length === 0 && <option value="">No published sales yet</option>}
      {ordered.map(s => <option key={s.sale_id} value={s.sale_id}>{label(s)}</option>)}
    </select></label>
    {sale && <div className="panel chosen-sale"><ProductImage product={product} small />
      <div><h2>{product?.name || sale.terms.product_id}</h2>
        <p className="small">{money(sale.terms)} · {date(sale.terms.starts_at)} – {date(sale.terms.ends_at)} · stock limit {sale.terms.stock}</p></div></div>}
    <Feedback query={windows} />
    {selection && <div className="tiles">
      <Tile label="Demand" value={totals.demand} hint="Purchase attempts received" />
      <Tile label="Throttled" value={totals.admission} hint="Rejected by the Admission Controller" tone="warn" />
      <Tile label="Sold out" value={totals.stock} hint="Rejected by Inventory: no stock left" tone="warn" />
      <Tile label="Reserved" value={totals.reservations} hint="Units reserved by Inventory" />
      <Tile label="Confirmed" value={totals.confirmed} hint="Paid and confirmed orders" tone="good" />
      <Tile label="Avg latency" value={latency === undefined ? "—" : `${latency} ms`} hint="Attempt to confirmed order" />
      <Tile label="Stock consumed" value={level ? `${level.sold} / ${level.stock}` : "—"} hint={level ? `${level.held} held · ${level.available} left` : "From Inventory"} />
    </div>}
    {selection && windows.data && data.length === 0 && <p className="empty">No finalized windows yet. Make a purchase, then wait about a minute.</p>}
    {recent.length > 0 && <div className="panel"><h2>Outcomes per minute</h2>
      <div className="legend">{parts.map(([key, name, cls]) => <span key={key}><i className={cls} />{name}</span>)}</div>
      <div className="stacked" role="img" aria-label={`Outcomes across the latest ${recent.length} windows`}>
        {recent.map(w => {
          const s = split(w);
          return <div className="column" key={w.starts_at} title={`${time(w.starts_at)} · demand ${w.demand}`}>
            <span className="column-value">{w.demand}</span>
            <div className="column-bar" style={{ height: `${w.demand / peak * 100}%` }}>
              {parts.map(([key, , cls]) => s[key] > 0 && <div key={key} className={cls} style={{ flexGrow: s[key] }} title={`${key}: ${s[key]}`} />)}
            </div>
            <span className="column-label">{time(w.starts_at)}</span>
          </div>;
        })}
      </div>
    </div>}
    {data.length > 0 && <div className="panel table-wrap"><h2>Window history</h2><table>
      <thead><tr><th>Minute</th><th>Demand</th><th>Throttled</th><th>Sold out</th><th>Reserved</th><th>Confirmed</th><th>Avg latency</th></tr></thead>
      <tbody>{[...data].reverse().map(w => <tr key={w.starts_at}><td>{time(w.starts_at)}–{time(w.ends_at)}</td><td>{w.demand}</td>
        <td>{w.admission_rejections}</td><td>{w.stock_rejections}</td><td>{w.reservations}</td><td>{w.confirmed_purchases}</td>
        <td>{w.average_latency_ms == null ? "—" : `${Math.round(w.average_latency_ms)} ms`}</td></tr>)}</tbody></table></div>}
  </section>;
}

function Tile({ label, value, hint, tone }: { label: string; value: number | string; hint: string; tone?: "good" | "warn" }) {
  return <div className={`tile ${tone || ""}`}><span>{label}</span><strong>{value}</strong><small>{hint}</small></div>;
}

type SaleTotals = { demand: number; confirmed: number; reserved: number; soldOut: number; throttled: number };
const zero: SaleTotals = { demand: 0, confirmed: 0, reserved: 0, soldOut: 0, throttled: 0 };
const add = (a: SaleTotals, w: Window): SaleTotals => ({ demand: a.demand + w.demand, confirmed: a.confirmed + w.confirmed_purchases,
  reserved: a.reserved + w.reservations, soldOut: a.soldOut + w.stock_rejections, throttled: a.throttled + w.admission_rejections });

/** Totals across every sale, read from each sale's analytics windows. */
function Overview({ sales, catalog, onPick }: { sales: Sale[]; catalog: Map<string, Product>; onPick: (id: string) => void }) {
  const [totals, setTotals] = useState<Record<string, SaleTotals>>({});
  const [failed, setFailed] = useState(false);
  const ids = sales.map(s => s.sale_id).join(",");
  useEffect(() => {
    let active = true;
    async function load() {
      const ids = sales.map(s => s.sale_id);
      const results = await Promise.allSettled(ids.map(id => api<Window[]>("/api/analytics/sales/" + segment(id))));
      if (!active) return;
      setFailed(results.some(r => r.status === "rejected"));
      setTotals(Object.fromEntries(results.map((r, i) => [ids[i], r.status === "fulfilled" ? r.value.reduce(add, zero) : zero])));
    }
    void load();
    const timer = window.setInterval(() => void load(), 10_000);
    return () => { active = false; window.clearInterval(timer); };
    // Reload when the set of sales changes, not on every render.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [ids]);
  const all = Object.values(totals).reduce((a, b) => ({ demand: a.demand + b.demand, confirmed: a.confirmed + b.confirmed,
    reserved: a.reserved + b.reserved, soldOut: a.soldOut + b.soldOut, throttled: a.throttled + b.throttled }), zero);
  const revenue = new Map<string, number>();
  for (const s of sales) revenue.set(s.terms.currency, (revenue.get(s.terms.currency) || 0) + (totals[s.sale_id]?.confirmed || 0) * s.terms.price_minor);
  const earned = [...revenue].filter(([, v]) => v > 0).map(([c, v]) => formatMoney(v, c)).join(" + ") || "—";
  const flash = sales.filter(s => isFlash(s.terms, catalog.get(s.terms.product_id))).length;
  const ranked = [...sales].sort((a, b) => (totals[b.sale_id]?.demand || 0) - (totals[a.sale_id]?.demand || 0));
  return <section className="stack overview" aria-label="All sales">
    <div className="section-head"><h2>All sales</h2><span className="small">{sales.length} sales · {flash} flash · {sales.length - flash} regular</span></div>
    {failed && <p className="small">Some sales have no analytics yet; their numbers are left out.</p>}
    <div className="tiles">
      <Tile label="Demand" value={all.demand} hint="Purchase attempts, all sales" />
      <Tile label="Confirmed" value={all.confirmed} hint="Paid orders" tone="good" />
      <Tile label="Revenue" value={earned} hint="Confirmed orders × price" tone="good" />
      <Tile label="Sold out" value={all.soldOut} hint="Rejected: no stock left" tone="warn" />
      <Tile label="Throttled" value={all.throttled} hint="Rejected: too many at once" tone="warn" />
      <Tile label="Cancelled" value={Math.max(0, all.reserved - all.confirmed)} hint="Reserved, then payment failed or still paying" />
    </div>
    {sales.length > 0 && <div className="panel table-wrap"><table>
      <thead><tr><th>Sale</th><th>Type</th><th>Demand</th><th>Confirmed</th><th>Revenue</th><th>Sold out</th><th>Throttled</th></tr></thead>
      <tbody>{ranked.map(s => {
        const n = totals[s.sale_id] || zero;
        const product = catalog.get(s.terms.product_id);
        return <tr key={s.sale_id}>
          <td><button className="link" onClick={() => onPick(s.sale_id)}>{product?.name || s.terms.product_id}</button>
            <span className="small"> · {date(s.terms.starts_at)}</span></td>
          <td>{isFlash(s.terms, product) ? <span className="pill flash">⚡ Flash</span> : <span className="pill">Regular</span>}</td>
          <td>{n.demand}</td><td>{n.confirmed}</td><td>{n.confirmed ? formatMoney(n.confirmed * s.terms.price_minor, s.terms.currency) : "—"}</td>
          <td>{n.soldOut}</td><td>{n.throttled}</td>
        </tr>;
      })}</tbody></table></div>}
  </section>;
}
