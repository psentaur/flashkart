import { useEffect, useRef, useState } from "react";
import type { Attempt, Credential, Notification, Product, Sale } from "./api";
import { useQuery } from "./hooks";
import type { Purchase } from "./session";
import { orderItems, saleByID } from "./session";

const seenIDs = (key: string): string[] => { try { return JSON.parse(sessionStorage.getItem(key) || "[]"); } catch { return []; } };

type OrderNote = { id: string; title: string; detail: string; tone: "good" | "bad" | "mixed"; key: string };

/** One message per order: it appears once every item in the order has a result from the Notification Service. */
function summarise(id: string, items: Purchase[], outcomes: Map<string, Attempt>, name: (p: Purchase) => string): OrderNote | undefined {
  const results = items.map(p => outcomes.get(p.attempt_id));
  if (results.some(r => !r)) return undefined;
  const confirmed = results.filter(r => r!.state === "CONFIRMED").length;
  const counts = new Map<string, number>();
  for (const p of items) counts.set(name(p), (counts.get(name(p)) || 0) + 1);
  const detail = [...counts].map(([n, c]) => c > 1 ? `${n} ×${c}` : n).join(", ");
  const units = `${items.length} item${items.length === 1 ? "" : "s"}`;
  const declined = results.every(r => r!.reason === "PAYMENT_DECLINED");
  const soldOut = results.every(r => r!.reason === "SOLD_OUT");
  const title = confirmed === items.length ? `Order confirmed · ${units}`
    : confirmed ? `Order partly confirmed · ${confirmed} of ${items.length} items`
      : declined ? `Payment declined · ${units}, not charged` : soldOut ? `Sold out · ${units}, not charged` : `Order not completed · ${units}, not charged`;
  return { id, title, detail, tone: confirmed === items.length ? "good" : confirmed ? "mixed" : "bad", key: `${id}:${confirmed}` };
}

/** Messages from the Notification Service about this shopper's own orders, with an unread count. */
export function Bell({ userID, token, purchases, onOpenOrder }: { userID: string; token: Credential; purchases: Purchase[]; onOpenOrder: (id?: string) => void }) {
  const notifications = useQuery<Notification[]>("/api/notifications", token, 4000);
  const sales = useQuery<Sale[]>("/api/sales", undefined, 30_000);
  const products = useQuery<Product[]>("/api/products", undefined, 30_000);
  const [open, setOpen] = useState(false);
  const seenKey = `flashkart.seen-notifications.${userID}`;
  const [seen, setSeen] = useState(() => seenIDs(seenKey));
  const box = useRef<HTMLDivElement>(null);
  const bySale = saleByID(sales.data);
  const catalog = new Map(products.data?.map(item => [item.product_id, item]));
  const outcomes = new Map((notifications.data || []).map(n => [n.attempt.attempt_id, n.attempt]));
  const name = (p: Purchase) => { const sale = bySale.get(p.sale_id); return (sale && catalog.get(sale.terms.product_id)?.name) || "Item"; };
  // Purchases are stored newest first; group them into the orders they were placed in.
  const orders = new Map<string, Purchase[]>();
  for (const p of orderItems(purchases, notifications.data).filter(p => p.admission === "admitted")) {
    const id = p.checkout || p.attempt_id;
    orders.set(id, [...(orders.get(id) || []), p]);
  }
  const list = [...orders].flatMap(([id, items]) => summarise(id, items, outcomes, name) ?? []);
  const unread = list.filter(n => !seen.includes(n.key)).length;

  useEffect(() => {
    if (!open) return;
    const close = (e: MouseEvent) => { if (!box.current?.contains(e.target as Node)) setOpen(false); };
    const escape = (e: KeyboardEvent) => { if (e.key === "Escape") setOpen(false); };
    document.addEventListener("mousedown", close);
    document.addEventListener("keydown", escape);
    return () => { document.removeEventListener("mousedown", close); document.removeEventListener("keydown", escape); };
  }, [open]);

  function toggle() {
    if (!open) {
      const ids = [...new Set([...seen, ...list.map(n => n.key)])];
      try { sessionStorage.setItem(seenKey, JSON.stringify(ids)); } catch { /* memory only */ }
      // Mark as read after opening, so this view still highlights what was new.
      window.setTimeout(() => setSeen(ids), 1500);
    }
    setOpen(!open);
  }

  return <div className="bell" ref={box}>
    <button className="secondary bell-button" aria-expanded={open} aria-label={`Notifications${unread ? `, ${unread} unread` : ""}`} onClick={toggle}>
      <span aria-hidden="true">🔔</span>{unread > 0 && <span className="count">{unread}</span>}
    </button>
    {open && <div className="bell-panel" role="dialog" aria-label="Notifications">
      <strong>Notifications</strong>
      {list.length === 0 && <p className="small">No updates yet. You will be told here when an order is confirmed or fails.</p>}
      <ul>{list.slice(0, 8).map(n => <li key={n.id}>
        <button className={`note-item ${n.tone} ${seen.includes(n.key) ? "" : "new"}`} onClick={() => { setOpen(false); onOpenOrder(n.id); }}>
          <strong>{n.title}</strong><span>{n.detail}</span></button></li>)}</ul>
      {list.length > 0 && <button className="link" onClick={() => { setOpen(false); onOpenOrder(); }}>See all orders</button>}
    </div>}
  </div>;
}
