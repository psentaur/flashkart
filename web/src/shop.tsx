import { useState } from "react";
import { discount, formatMoney, isFlash, money, statusOf } from "./api";
import type { Product, Sale, Stock } from "./api";
import { useQuery } from "./hooks";
import type { CartLine } from "./session";
import { Feedback, ProductImage, StockBar, countdown, useNow } from "./ui";

type Props = {
  cart: CartLine[]; setCart: (update: (cart: CartLine[]) => CartLine[]) => void;
  onCart: () => void; onSignIn?: () => void;
};

export function Shop({ cart, setCart, onCart, onSignIn }: Props) {
  const products = useQuery<Product[]>("/api/products");
  const sales = useQuery<Sale[]>("/api/sales", undefined, 10_000);
  const stock = useQuery<Stock[]>("/api/inventory/sales", undefined, 3000);
  const now = useNow();
  const [added, setAdded] = useState<string>();
  const [sort, setSort] = useState<Sorting>(DEFAULT_SORT);
  const catalog = new Map(products.data?.map(item => [item.product_id, item]));
  const levels = new Map(stock.data?.map(item => [item.sale_id, item]));
  const status = (s: Sale) => statusOf(s, levels.get(s.sale_id), now);
  const flash = (sales.data || []).filter(s => isFlash(s.terms, catalog.get(s.terms.product_id)));
  const listings = (sales.data || []).filter(s => !isFlash(s.terms, catalog.get(s.terms.product_id)) && status(s) === "Live");
  const live = flash.filter(s => status(s) === "Live").sort((a, b) => a.terms.ends_at.localeCompare(b.terms.ends_at));
  const upcoming = flash.filter(s => status(s) === "Upcoming").sort((a, b) => a.terms.starts_at.localeCompare(b.terms.starts_at));
  // Each product appears once: as a flash deal while one is live, otherwise at its regular listing.
  // A sold-out flash deal gives way to the regular listing, if there is one.
  const shelf = (products.data || []).flatMap(item => {
    const deal = live.find(s => s.terms.product_id === item.product_id);
    const listing = listings.find(s => s.terms.product_id === item.product_id);
    const usable = deal && (levels.get(deal.sale_id)?.available ?? 1) > 0 ? deal : listing ? undefined : deal;
    const best = usable || listing;
    return best ? [{ item, sale: best, onSale: !!usable, price: best.terms.price_minor }] : [];
  });
  sortProducts(shelf, sort);
  const liveDeals = shelf.filter(entry => entry.onSale).length;

  function add(sale: Sale, quantity: number) {
    if (onSignIn) return onSignIn();
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
    <section className={`hero ${liveDeals ? "on" : ""}`}>
      <div>
        <span className="hero-tag">⚡ Flash sales</span>
        <h1>{liveDeals ? `${liveDeals} deal${liveDeals === 1 ? " is" : "s are"} live now` : upcoming.length ? "Flash deals are coming soon" : "No flash deals right now"}</h1>
        <p>Limited stock at flash prices. Each deal has its own timer — first come, first served, and stock updates live as other shoppers buy.</p>
      </div>
    </section>
    <Feedback query={sales} />
    <section>
      <div className="section-head"><div><h2>Products</h2>
        <p>Everything you can buy right now. ⚡ Flash deals are marked, with their discount and a timer.</p></div>
        <SortPicker value={sort} onChange={setSort} /></div>
      <Feedback query={products} />
      <Feedback query={stock} />
      {products.data && sales.data && shelf.length === 0 && <p className="empty">Nothing is for sale right now. Check back soon.</p>}
      <div className="cards">{shelf.map(({ item, sale, onSale }) => onSale ? card(sale)
        : <RegularCard key={item.product_id} product={item} sale={sale} stock={levels.get(sale.sale_id)}
          inCart={cart.find(line => line.sale_id === sale.sale_id)?.quantity || 0} justAdded={added === sale.sale_id} onAdd={add} onCart={onCart} />)}</div>
    </section>
    {upcoming.length > 0 && <section><h2 className="spaced">Coming soon</h2><div className="cards compact">{upcoming.map(card)}</div></section>}
  </div>;
}

function RegularCard({ product, sale, stock, inCart, justAdded, onAdd, onCart }: {
  product: Product; sale: Sale; stock?: Stock; inCart: number; justAdded: boolean;
  onAdd: (sale: Sale, quantity: number) => void; onCart: () => void;
}) {
  return <article className="product-card" aria-label={product.name}>
    <ProductImage product={product} />
    <div className="card-content"><h3>{product.name}</h3>
      {product.description && <p className="clamp">{product.description}</p>}
      <strong className="price small-price">{money(sale.terms)}</strong>
      <StockBar stock={stock} limit={sale.terms.stock} />
      <BuyControls sale={sale} name={product.name} stock={stock} inCart={inCart} justAdded={justAdded} onAdd={onAdd} onCart={onCart} />
    </div>
  </article>;
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

export type Sorting = { group: "sale" | "regular" | "none"; price: "low" | "high" };
export const DEFAULT_SORT: Sorting = { group: "sale", price: "low" };

/** Sorts by sale group first (if any), then by price. */
export function sortProducts<T extends { onSale: boolean; price: number }>(items: T[], sort: Sorting) {
  const group = (x: T) => sort.group === "none" ? 0 : (x.onSale === (sort.group === "sale") ? 0 : 1);
  return items.sort((a, b) => group(a) - group(b) || (sort.price === "low" ? a.price - b.price : b.price - a.price));
}

export function SortPicker({ value, onChange, groups = { sale: "On sale first", regular: "Not on sale first" } }: {
  value: Sorting; onChange: (sort: Sorting) => void; groups?: { sale: string; regular: string };
}) {
  return <div className="sort-controls">
    <label className="sort">Show<select value={value.group} onChange={e => onChange({ ...value, group: e.target.value as Sorting["group"] })}>
      <option value="sale">{groups.sale}</option><option value="regular">{groups.regular}</option><option value="none">All mixed</option></select></label>
    <label className="sort">Price<select value={value.price} onChange={e => onChange({ ...value, price: e.target.value as Sorting["price"] })}>
      <option value="low">Low to high</option><option value="high">High to low</option></select></label>
  </div>;
}
