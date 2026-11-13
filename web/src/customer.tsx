import { useEffect, useState } from "react";
import { ApiError, api, date, message, money, operationID, segment } from "./api";
import type { Attempt, Notification, Order, Product, ProductView, Sale } from "./api";
import { useQuery } from "./hooks";
import { Feedback } from "./ui";

export function Store({ token, intents }: { token: string; intents: Map<string, Intent> }) {
  const products = useQuery<Product[]>("/api/products");
  const sales = useQuery<Sale[]>("/api/sales");
  const [selection, setSelection] = useState<{ product: string; sale?: string }>();
  const product = useQuery<ProductView>(selection ? "/api/products/" + segment(selection.product) : null);
  const sale = useQuery<Sale>(selection?.sale ? "/api/sales/" + segment(selection.sale) : null);
  const [now, setNow] = useState(Date.now);
  useEffect(() => {
    const timer = window.setInterval(() => setNow(Date.now()), 1000);
    return () => window.clearInterval(timer);
  }, []);
  const catalog = new Map(products.data?.map(item => [item.product_id, item]));

  return <div className="stack">
    <section>
      <div className="section-head"><div><h1>Flash sales</h1><p>Browse published drops and reserve limited stock.</p></div>
        <button className="secondary" onClick={() => { products.reload(); sales.reload(); }}>Refresh catalog</button></div>
      <Feedback query={sales} />
      {sales.data?.length === 0 && <p className="empty">No published sales yet.</p>}
      <div className="cards">{sales.data?.map(item => {
        const status = now >= Date.parse(item.terms.ends_at) ? "Ended" : now < Date.parse(item.terms.starts_at) ? "Upcoming" : "Live";
        const image = catalog.get(item.terms.product_id)?.image_url;
        return <article className="sale-card" key={item.sale_id}>
          {image && <img src={image} alt="" loading="lazy" />}
          <div className="card-content"><span className={`pill ${status === "Live" ? "live" : ""}`}>{status}</span>
            <h2>{catalog.get(item.terms.product_id)?.name || item.terms.product_id}</h2>
            <p>{status === "Upcoming" ? "Starts" : "Ends"} {date(status === "Upcoming" ? item.terms.starts_at : item.terms.ends_at)}</p>
            <div className="card-bottom"><strong>{money(item.terms)}</strong>
              <button onClick={() => setSelection({ product: item.terms.product_id, sale: item.sale_id })}>View sale</button></div>
          </div>
        </article>;
      })}</div>
    </section>
    {selection && <section className="panel" aria-label="Sale and product details">
      <div className="section-head"><h2>{product.data?.product.name || selection.product}</h2>
        <button className="secondary" onClick={() => setSelection(undefined)}>Close details</button></div>
      <Feedback query={product} /><Feedback query={sale} />
      {product.data?.product.description && <p>{product.data.product.description}</p>}
      {sale.data && <><SaleTerms sale={sale.data} />
        <Purchase key={sale.data.sale_id} sale={sale.data} token={token} now={now} intents={intents} /></>}
    </section>}
    <section><h2>Product catalog</h2><Feedback query={products} />
      {products.data?.length === 0 && <p className="empty">No products yet.</p>}
      <div className="catalog-list">{products.data?.map(item => <button className="secondary" key={item.product_id}
        onClick={() => setSelection({ product: item.product_id })}>{item.name}</button>)}</div>
    </section>
  </div>;
}

export function SaleTerms({ sale }: { sale: Sale }) {
  return <dl className="facts">
    <div><dt>Sale ID</dt><dd>{sale.sale_id}</dd></div>
    <div><dt>Price</dt><dd>{money(sale.terms)}</dd></div>
    <div><dt>Published stock limit</dt><dd>{sale.terms.stock}</dd></div>
    <div><dt>Starts</dt><dd>{date(sale.terms.starts_at)}</dd></div>
    <div><dt>Ends</dt><dd>{date(sale.terms.ends_at)}</dd></div>
  </dl>;
}

type Intent = { id: string; accepted?: Attempt; retryAt?: number; sending?: boolean };
export const newIntents = () => new Map<string, Intent>();

function Purchase({ sale, token, now, intents }: { sale: Sale; token: string; now: number; intents: Map<string, Intent> }) {
  const key = sale.sale_id;
  const [, redraw] = useState(0);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>();
  const intent = intents.get(key);
  useEffect(() => {
    if (!intent?.sending && !(intent?.retryAt && intent.retryAt > Date.now())) return;
    const timer = window.setInterval(() => redraw(value => value + 1), 250);
    return () => window.clearInterval(timer);
  }, [intent?.sending, intent?.retryAt]);

  async function purchase() {
    const previous = intents.get(key);
    if (!token || previous?.sending || previous?.accepted || Date.now() < (previous?.retryAt || 0)) return;
    const next = previous || { id: operationID() };
    intents.set(key, next);
    next.sending = true;
    setBusy(true);
    setError(undefined);
    try {
      next.accepted = await api<Attempt>("/api/attempts", {
        method: "POST", token, attemptID: next.id,
        body: { attempt_id: next.id, sale_id: sale.sale_id },
      });
    } catch (error) {
      if (error instanceof ApiError && error.status === 429) {
        next.retryAt = Date.now() + error.retryAfter * 1000;
      }
      setError(error);
    } finally {
      next.sending = false;
      setBusy(false);
      redraw(value => value + 1);
    }
  }

  const waiting = (intent?.retryAt || 0) > Date.now();
  const outsideWindow = now < Date.parse(sale.terms.starts_at) || now >= Date.parse(sale.terms.ends_at);
  return <div className="purchase">
    {intent?.accepted ? <AttemptStatus attempt={intent.accepted} token={token}
      onNew={() => { intents.delete(key); setError(undefined); redraw(value => value + 1); }} /> : <>
      {!token && <p>Add your customer token to reserve stock.</p>}
      <button disabled={!token || busy || intent?.sending || waiting || (!intent && outsideWindow)} onClick={() => void purchase()}>
        {busy || intent?.sending ? "Submitting…" : intent ? "Retry same attempt" : "Reserve one unit"}
      </button>
      {error !== undefined && <p role="alert">{message(error)}</p>}
    </>}
  </div>;
}

function AttemptStatus({ attempt, token, onNew }: { attempt: Attempt; token: string; onNew: () => void }) {
  const status = useQuery<Attempt>(`/api/attempts/${segment(attempt.attempt_id)}`, token, 1300,
    value => value.state !== "QUEUED");
  const current = status.data || attempt;
  return <div aria-live="polite">
    <Outcome attempt={current} token={token} />
    {current.state === "QUEUED" && <p>Waiting for a terminal outcome.</p>}
    <Feedback query={status} />
    {current.state !== "QUEUED" && <button className="secondary" onClick={onNew}>Start another attempt</button>}
  </div>;
}

function Outcome({ attempt, token }: { attempt: Attempt; token: string }) {
  const [expanded, setExpanded] = useState(false);
  const order = useQuery<Order>(expanded && attempt.order_id ? "/api/orders/" + segment(attempt.order_id) : null, token);
  return <div className="outcome">
    <span className={`pill ${attempt.state === "CONFIRMED" ? "live" : ""}`}>{attempt.state}</span>
    {attempt.reason && <span> {attempt.reason.replaceAll("_", " ").toLowerCase()}</span>}
    <p className="identifier">Attempt {attempt.attempt_id}</p>
    {attempt.order_id && <button className="secondary" onClick={() => setExpanded(value => !value)}>{expanded ? "Hide order" : "View order"}</button>}
    <Feedback query={order} />
    {order.data && <p>Order <strong>{order.data.order_id}</strong> · {order.data.state}</p>}
  </div>;
}

export function Activity({ token }: { token: string }) {
  const notifications = useQuery<Notification[]>(token ? "/api/notifications" : null, token, 4000);
  return <section>
    <div className="section-head"><div><h1>Purchase activity</h1><p>Terminal outcomes for the signed-in demo customer.</p></div>
      {token && <button className="secondary" onClick={notifications.reload}>Refresh notifications</button>}</div>
    {!token && <p className="empty">Add your customer token to see notifications.</p>}
    <Feedback query={notifications} />
    {notifications.data?.length === 0 && <p className="empty">No notifications yet.</p>}
    <div className="stack">{notifications.data?.map(item => <article className="panel" key={item.event_id}>
      <h2>Sale {item.attempt.sale_id}</h2><Outcome attempt={item.attempt} token={token} />
    </article>)}</div>
  </section>;
}
