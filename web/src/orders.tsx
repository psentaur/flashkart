import { useEffect } from "react";
import { date, formatMoney } from "./api";
import type { Attempt, Credential, Notification, Product, Sale } from "./api";
import { useQuery } from "./hooks";
import type { Purchase } from "./session";
import { orderItems, saleByID } from "./session";
import { Feedback, ProductImage, Tracker } from "./ui";
import type { StepState } from "./ui";

type Outcome = { key: string; chip: string; tone: "good" | "bad" | "wait"; note: string; steps: { label: string; state: StepState }[] };

/** Where one item's order is, in shopper language, from its admission result and delivered notification. */
export function outcomeOf(purchase: Purchase, attempt?: Attempt): Outcome {
  const steps = (states: StepState[], labels = ["Order placed", "Item reserved", "Payment", "Confirmed"]) =>
    labels.map((label, i) => ({ label, state: states[i] }));
  if (purchase.admission === "throttled") return { key: "busy", chip: "Not placed", tone: "bad",
    note: "Too many shoppers at once — the store could not take your order. Please try again.", steps: steps(["failed", "skipped", "skipped", "skipped"]) };
  if (purchase.admission !== "admitted") return { key: "failed", chip: "Not placed", tone: "bad",
    note: purchase.error || "Your order could not be placed.", steps: steps(["failed", "skipped", "skipped", "skipped"]) };
  if (!attempt || attempt.state === "QUEUED") return { key: "wait", chip: "Processing…", tone: "wait",
    note: "We are reserving your item and taking payment.", steps: steps(["done", "active", "waiting", "waiting"]) };
  if (attempt.state === "CONFIRMED") return { key: "ok", chip: "Confirmed", tone: "good",
    note: "Paid and confirmed. You got it!", steps: steps(["done", "done", "done", "done"]) };
  if (attempt.state === "REJECTED") {
    const ended = attempt.reason === "ENDED" || attempt.reason === "INACTIVE";
    return { key: "rej-" + attempt.reason, chip: ended ? "Sale not active" : "Sold out", tone: "bad",
      note: ended ? "The sale had ended or not started when your order arrived. You were not charged."
        : "Every unit was claimed before your order. You were not charged.", steps: steps(["done", "failed", "skipped", "skipped"]) };
  }
  const declined = attempt.reason === "PAYMENT_DECLINED";
  return { key: "can-" + attempt.reason, chip: declined ? "Payment declined" : "Cancelled", tone: "bad",
    note: declined ? "Your card was declined, so nothing was charged and the item went back on sale."
      : "Payment did not complete in time, so the item went back on sale.", steps: steps(["done", "done", "failed", "skipped"]) };
}

export function Orders({ token, purchases, focus, onShop }: { token: Credential; purchases: Purchase[]; focus?: string; onShop: () => void }) {
  const products = useQuery<Product[]>("/api/products");
  const sales = useQuery<Sale[]>("/api/sales");
  const notifications = useQuery<Notification[]>("/api/notifications", token, 2000);
  const catalog = new Map(products.data?.map(item => [item.product_id, item]));
  const bySale = saleByID(sales.data);
  const delivered = new Map(notifications.data?.map(item => [item.attempt.attempt_id, item.attempt]));
  const mine = orderItems(purchases, notifications.data);
  // Opening an order from a notification scrolls to it once the page has rendered.
  useEffect(() => {
    if (focus) document.getElementById(`order-${focus}`)?.scrollIntoView({ behavior: "smooth", block: "start" });
  }, [focus, notifications.data]);
  const orders = new Map<string, Purchase[]>();
  for (const p of mine) orders.set(p.checkout || p.attempt_id, [...(orders.get(p.checkout || p.attempt_id) || []), p]);

  return <section className="stack">
    <div className="section-head"><div><h1>My orders</h1><p>Your orders and what happened to each item.</p></div>
      <button className="secondary" onClick={() => notifications.reload()}>Refresh</button></div>
    <Feedback query={notifications} />
    {notifications.data && !mine.length && <><p className="empty">No orders yet.</p><div><button onClick={onShop}>Start shopping</button></div></>}
    {[...orders.entries()].map(([id, items]) => {
      // Items with the same product and the same outcome are shown once with a count.
      const groups = new Map<string, { purchase: Purchase; outcome: Outcome; count: number }>();
      for (const p of items) {
        const outcome = outcomeOf(p, delivered.get(p.attempt_id));
        const key = p.sale_id + ":" + outcome.key;
        const group = groups.get(key);
        if (group) group.count++; else groups.set(key, { purchase: p, outcome, count: 1 });
      }
      const all = [...groups.values()];
      const confirmed = all.filter(g => g.outcome.tone === "good");
      const pending = all.some(g => g.outcome.tone === "wait");
      const paid = new Map<string, number>();
      for (const group of confirmed) {
        const terms = bySale.get(group.purchase.sale_id)?.terms;
        if (terms) paid.set(terms.currency, (paid.get(terms.currency) || 0) + terms.price_minor * group.count);
      }
      const paidTotals = [...paid].map(([currency, amount]) => formatMoney(amount, currency)).join(" + ");
      const placed = items[0].created_at;
      const label = placed ? `Order · ${date(placed)}` : `Order · ${id.slice(0, 8)}`;
      const status = pending ? "Processing…" : confirmed.length === all.length ? "Confirmed" : confirmed.length ? "Partly confirmed" : "Not completed";
      return <article className={`panel order ${focus === id ? "focused" : ""}`} key={id} id={`order-${id}`} aria-label={label}>
        <div className="order-top"><div><h2>{label}</h2>
          <p className="small">{items.length} item{items.length === 1 ? "" : "s"}{paidTotals && ` · paid ${paidTotals}`}</p></div>
          <span className={`pill ${pending ? "soon" : confirmed.length === all.length ? "live" : confirmed.length ? "soon" : "bad"}`}>{status}</span></div>
        {all.map(({ purchase, outcome, count }) => {
          const sale = bySale.get(purchase.sale_id);
          const product = catalog.get(purchase.product_id || sale?.terms.product_id || "");
          return <div className="order-line" key={purchase.sale_id + outcome.key}>
            <ProductImage product={product} small />
            <div className="order-info">
              <div className="order-name"><h3>{product?.name || "Item"}{count > 1 && <span className="times"> × {count}</span>}</h3>
                <span className={`pill ${outcome.tone === "good" ? "live" : outcome.tone === "wait" ? "soon" : "bad"}`}>{outcome.chip}</span></div>
              {sale && <p className="small">{formatMoney(sale.terms.price_minor * count, sale.terms.currency)}{purchase.payment === "decline" && " · test card"}</p>}
              <Tracker steps={outcome.steps} />
              <p className="small note">{outcome.note}</p>
            </div>
          </div>;
        })}
      </article>;
    })}
  </section>;
}
