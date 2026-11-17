import { useEffect, useRef, useState } from "react";
import type { Notification, Product, Sale } from "./api";
import { useQuery } from "./hooks";
import type { Purchase } from "./session";
import { saleByID } from "./session";

const SEEN = "flashkart.seen-notifications";
const seenIDs = (): string[] => { try { return JSON.parse(sessionStorage.getItem(SEEN) || "[]"); } catch { return []; } };

function text(n: Notification, name: string) {
  const a = n.attempt;
  if (a.state === "CONFIRMED") return { title: `${name}: order confirmed`, tone: "good" };
  if (a.state === "REJECTED") return { title: a.reason === "SOLD_OUT" ? `${name}: sold out before your order` : `${name}: the sale was not running`, tone: "bad" };
  if (a.reason === "PAYMENT_DECLINED") return { title: `${name}: payment declined, not charged`, tone: "bad" };
  return { title: `${name}: order cancelled, not charged`, tone: "bad" };
}

/** Messages from the Notification Service about this shopper's own orders, with an unread count. */
export function Bell({ token, purchases, onOpenOrders }: { token: string; purchases: Purchase[]; onOpenOrders: () => void }) {
  const notifications = useQuery<Notification[]>("/api/notifications", token, 4000);
  const sales = useQuery<Sale[]>("/api/sales", undefined, 30_000);
  const products = useQuery<Product[]>("/api/products", undefined, 30_000);
  const [open, setOpen] = useState(false);
  const [seen, setSeen] = useState<string[]>(seenIDs);
  const box = useRef<HTMLDivElement>(null);
  const mine = new Set(purchases.filter(p => !p.crowd).map(p => p.attempt_id));
  const bySale = saleByID(sales.data);
  const catalog = new Map(products.data?.map(item => [item.product_id, item]));
  const list = (notifications.data || []).filter(n => mine.has(n.attempt.attempt_id)).reverse();
  const unread = list.filter(n => !seen.includes(n.event_id)).length;

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
      const ids = [...new Set([...seen, ...list.map(n => n.event_id)])];
      try { sessionStorage.setItem(SEEN, JSON.stringify(ids)); } catch { /* memory only */ }
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
      <ul>{list.slice(0, 8).map(n => {
        const sale = bySale.get(n.attempt.sale_id);
        const name = (sale && catalog.get(sale.terms.product_id)?.name) || "Your item";
        const { title, tone } = text(n, name);
        return <li key={n.event_id} className={`${tone} ${seen.includes(n.event_id) ? "" : "new"}`}>{title}</li>;
      })}</ul>
      {list.length > 0 && <button className="link" onClick={() => { setOpen(false); onOpenOrders(); }}>See all orders</button>}
    </div>}
  </div>;
}
