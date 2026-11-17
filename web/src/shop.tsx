import { useState } from "react";
import { discount, formatMoney, isFlash, money, statusOf } from "./api";
import type { Product, Sale, Stock } from "./api";
import { useQuery } from "./hooks";
import type { CartLine } from "./session";
import { Feedback, ProductImage, StockBar, countdown, useNow } from "./ui";

type Props = { cart: CartLine[]; setCart: (update: (cart: CartLine[]) => CartLine[]) => void; onCart: () => void };

export function Shop({ cart, setCart, onCart }: Props) {
  const products = useQuery<Product[]>("/api/products");
  const sales = useQuery<Sale[]>("/api/sales", undefined, 10_000);
  const stock = useQuery<Stock[]>("/api/inventory/sales", undefined, 3000);
  const now = useNow();
  const [added, setAdded] = useState<string>();
  const [sort, setSort] = useState<SortKey>("sale");
  const catalog = new Map(products.data?.map(item => [item.product_id, item]));
  const levels = new Map(stock.data?.map(item => [item.sale_id, item]));
  // Flash deals get their own section; regular listings make products in the catalog buyable.
  const all = (sales.data || []).filter(s => isFlash(s.terms, catalog.get(s.terms.product_id)));
  const status = (s: Sale) => statusOf(s, levels.get(s.sale_id), now);
  const listings = (sales.data || []).filter(s => !isFlash(s.terms, catalog.get(s.terms.product_id)) && status(s) === "Live");
  const live = all.filter(s => status(s) === "Live").sort((a, b) => a.terms.ends_at.localeCompare(b.terms.ends_at));
  const upcoming = all.filter(s => status(s) === "Upcoming").sort((a, b) => a.terms.starts_at.localeCompare(b.terms.starts_at));
  const ended = all.filter(s => status(s) === "Ended").sort((a, b) => b.terms.ends_at.localeCompare(a.terms.ends_at)).slice(0, 6);
  // Customers only see products that can be bought now: those with a live regular listing or flash deal.
  const shelf = (products.data || []).flatMap(item => {
    const deal = live.find(s => s.terms.product_id === item.product_id);
    const listing = listings.find(s => s.terms.product_id === item.product_id);
    // A sold-out flash deal gives way to the regular listing, if there is one.
    const usable = deal && (levels.get(deal.sale_id)?.available ?? 1) > 0 ? deal : listing ? undefined : deal;
    const best = usable || listing;
    return best ? [{ item, deal: usable, listing, price: best.terms.price_minor }] : [];
  });
  shelf.sort((a, b) => sort === "low" ? a.price - b.price : sort === "high" ? b.price - a.price
    : Number(!!b.deal) - Number(!!a.deal) || a.price - b.price);

  function add(sale: Sale, quantity: number) {
    setCart(previous => {
      const line = previous.find(item => item.sale_id === sale.sale_id);
      return line ? previous.map(item => item === line ? { ...item, quantity: item.quantity + quantity } : item)
        : [...previous, { sale_id: sale.sale_id, quantity }];
    });
    setAdded(sale.sale_id);
  }

  const card = (sale: Sale) => <DealCard key={sale.sale_id} sale={sale} product={catalog.get(sale.terms.product_id)} now={now}
    stock={levels.get(sale.sale_id)} inCart={cart.find(line => line.sale_id === sale.sale_id)?.quantity || 0}
    justAdded={added === sale.sale_id} onAdd={add} onCart={onCart} />;

  return <div className="stack">
    <section className={`hero ${live.length ? "on" : ""}`}>
      <div>
        <span className="hero-tag">⚡ Flash sales</span>
        <h1>{live.length ? `${live.length} deal${live.length === 1 ? " is" : "s are"} live now` : upcoming.length ? "Flash deals are coming soon" : "No flash deals right now"}</h1>
        <p>Limited stock at flash prices. Each deal has its own timer — first come, first served, and stock updates live as other shoppers buy.</p>
      </div>
    </section>
    <Feedback query={sales} />

    {live.length > 0 && <section><div className="section-head"><h2>Live now</h2>
      <button className="secondary" onClick={() => { products.reload(); sales.reload(); stock.reload(); }}>Refresh</button></div>
      <div className="cards">{live.map(card)}</div></section>}
    {upcoming.length > 0 && <section><h2 className="spaced">Coming soon</h2><div className="cards">{upcoming.map(card)}</div></section>}
    {ended.length > 0 && <section><h2 className="spaced">Recently ended</h2><div className="cards compact">{ended.map(card)}</div></section>}

    <section className="catalog">
      <div className="section-head"><div><h2>All products</h2>
        <p>Everything you can buy right now. ⚡ marks products that are cheaper in a flash sale.</p></div>
        <SortPicker value={sort} onChange={setSort} /></div>
      <Feedback query={products} />
      {products.data && shelf.length === 0 && <p className="empty">Nothing is for sale right now. Check back soon.</p>}
      <div className="cards compact">{shelf.map(({ item, deal, listing }) => {
        const buy = deal || listing!;
        const off = deal ? discount(deal.terms, item.regular_price) : 0;
        return <article className="product-card" key={item.product_id} aria-label={`${item.name} in catalog`}>
          <div className="deal-image"><ProductImage product={item} />{off > 0 && <span className="badge-off">−{off}%</span>}</div>
          <div className="card-content"><h3>{item.name}</h3>
            {item.description && <p className="clamp">{item.description}</p>}
            <div className="prices"><strong className="price small-price">{money(buy.terms)}</strong>
              {deal && item.regular_price && off > 0 && <s className="was">{formatMoney(item.regular_price.amount_minor, item.regular_price.currency)}</s>}</div>
            {deal && <span className="pill flash">⚡ Flash deal · ends in {countdown(deal.terms.ends_at, now)}</span>}
            <StockBar stock={levels.get(buy.sale_id)} limit={buy.terms.stock} />
            <BuyControls sale={buy} name={item.name} stock={levels.get(buy.sale_id)} flash={!!deal}
              inCart={cart.find(line => line.sale_id === buy.sale_id)?.quantity || 0} justAdded={added === buy.sale_id} onAdd={add} onCart={onCart} />
          </div>
        </article>;
      })}</div>
    </section>
  </div>;
}

function DealCard({ sale, product, stock, now, inCart, justAdded, onAdd, onCart }: {
  sale: Sale; product?: Product; stock?: Stock; now: number; inCart: number; justAdded: boolean;
  onAdd: (sale: Sale, quantity: number) => void; onCart: () => void;
}) {
  const status = statusOf(sale, stock, now);
  const off = discount(sale.terms, product?.regular_price);
  const soldOut = stock !== undefined && stock.available <= 0;
  const name = product?.name || sale.terms.product_id;
  return <article className={`deal ${status.toLowerCase()} ${soldOut ? "sold" : ""}`} aria-label={name}>
    <div className="deal-image"><ProductImage product={product} />
      <span className="ribbon">{status === "Live" ? (soldOut ? "Sold out" : "⚡ Flash deal") : status === "Upcoming" ? "Coming soon" : "Ended"}</span>
      {off > 0 && status !== "Ended" && <span className="badge-off">−{off}%</span>}</div>
    <div className="card-content">
      <h3>{name}</h3>
      {product?.description && <p className="clamp">{product.description}</p>}
      <div className="price-row"><div className="prices"><strong className="price">{money(sale.terms)}</strong>
        {off > 0 && product?.regular_price && <><s className="was">{formatMoney(product.regular_price.amount_minor, product.regular_price.currency)}</s><span className="off">{off}% off</span></>}</div>
        {status !== "Ended" && <span className="timer">{status === "Live" ? "Ends in" : "Starts in"} <b>{countdown(status === "Live" ? sale.terms.ends_at : sale.terms.starts_at, now)}</b></span>}</div>
      {status !== "Upcoming" && <StockBar stock={stock} limit={sale.terms.stock} />}
      {status === "Upcoming" && <p className="small">{sale.terms.stock} units at this price</p>}
      {status === "Live" && <BuyControls sale={sale} name={name} stock={stock} inCart={inCart} justAdded={justAdded} onAdd={onAdd} onCart={onCart} flash />}
    </div>
  </article>;
}

/** Quantity picker and Add to cart for a live sale, limited by the stock that is left. */
function BuyControls({ sale, name, stock, inCart, justAdded, onAdd, onCart, flash }: {
  sale: Sale; name: string; stock?: Stock; inCart: number; justAdded: boolean;
  onAdd: (sale: Sale, quantity: number) => void; onCart: () => void; flash?: boolean;
}) {
  const [quantity, setQuantity] = useState(1);
  const available = stock ? Math.max(0, stock.available) : sale.terms.stock;
  const max = Math.max(0, available - inCart);
  const qty = Math.max(1, Math.min(quantity, max || 1));
  if (stock !== undefined && available === 0) return <button disabled>Sold out</button>;
  return <>
    <div className="buy-row">
      <div className="quantity" aria-label={`Quantity of ${name}`}>
        <button className="secondary" aria-label="Decrease quantity" disabled={qty <= 1} onClick={() => setQuantity(qty - 1)}>−</button>
        <span aria-live="polite">{qty}</span>
        <button className="secondary" aria-label="Increase quantity" disabled={qty >= max} onClick={() => setQuantity(qty + 1)}>+</button>
      </div>
      <button className={flash ? "flash-button" : ""} disabled={max === 0} onClick={() => { onAdd(sale, qty); setQuantity(1); }}>
        {max === 0 ? "All left are in your cart" : "Add to cart"}</button>
    </div>
    {(inCart > 0 || justAdded) && <p className="small">{inCart > 0 && <button className="link" onClick={onCart}>{inCart} in your cart</button>}
      {justAdded && <span className="added" role="status"> · Added ✓</span>}</p>}
  </>;
}

export type SortKey = "sale" | "low" | "high";

export function SortPicker({ value, onChange, labels = { sale: "On sale first" } }: { value: string; onChange: (key: SortKey) => void; labels?: { sale: string } }) {
  return <label className="sort">Sort by<select value={value} onChange={e => onChange(e.target.value as SortKey)}>
    <option value="sale">{labels.sale}</option><option value="low">Price: low to high</option><option value="high">Price: high to low</option>
  </select></label>;
}
