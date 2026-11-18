import { useRef, useState } from "react";
import { discount, formatMoney, money, operationID, statusOf } from "./api";
import type { Product, Sale, Stock } from "./api";
import { submitAttempt } from "./checkout";
import { useQuery } from "./hooks";
import type { CartLine, PaymentChoice, Purchase } from "./session";
import { saleByID } from "./session";
import { Feedback, ProductImage, StockBar } from "./ui";

type Props = {
  token: string; cart: CartLine[]; setCart: (update: (cart: CartLine[]) => CartLine[]) => void;
  onPurchased: (purchases: Purchase[]) => void; onShop: () => void;
};

export function Cart({ token, cart, setCart, onPurchased, onShop }: Props) {
  const products = useQuery<Product[]>("/api/products");
  const sales = useQuery<Sale[]>("/api/sales");
  const stock = useQuery<Stock[]>("/api/inventory/sales", undefined, 3000);
  const [payment, setPayment] = useState<PaymentChoice>("card");
  const [busy, setBusy] = useState(false);
  // A ref, not state: a double click arrives before React re-renders the disabled button.
  const placing = useRef(false);
  const [progress, setProgress] = useState("");
  const catalog = new Map(products.data?.map(item => [item.product_id, item]));
  const bySale = saleByID(sales.data);
  const levels = new Map(stock.data?.map(item => [item.sale_id, item]));
  const lines = cart.map(line => ({ line, sale: bySale.get(line.sale_id) }));
  const currencies = new Set(lines.map(({ sale }) => sale?.terms.currency).filter(Boolean));
  const total = lines.reduce((sum, { line, sale }) => sum + (sale ? sale.terms.price_minor * line.quantity : 0), 0);
  const units = lines.reduce((sum, { line }) => sum + line.quantity, 0);
  const saved = lines.reduce((sum, { line, sale }) => {
    const regular = sale && catalog.get(sale.terms.product_id)?.regular_price;
    return sum + (sale && regular && discount(sale.terms, regular) > 0 ? (regular.amount_minor - sale.terms.price_minor) * line.quantity : 0);
  }, 0);
  const unavailable = lines.some(({ sale }) => !sale || statusOf(sale, levels.get(sale.sale_id)) !== "Live");

  const setQuantity = (saleID: string, quantity: number) => setCart(previous =>
    quantity <= 0 ? previous.filter(item => item.sale_id !== saleID) : previous.map(item => item.sale_id === saleID ? { ...item, quantity } : item));

  async function checkout() {
    if (placing.current || unavailable || !units) return;
    placing.current = true;
    setBusy(true);
    const placed: Purchase[] = [];
    const order = operationID();
    let n = 0;
    // One attempt per unit, in order, so the Admission Controller sees a normal customer, not a burst.
    for (const { line, sale } of lines) {
      for (let i = 0; i < line.quantity && sale; i++) {
        setProgress(`Placing item ${++n} of ${units}…`);
        placed.push({ ...await submitAttempt(token, sale, payment), checkout: order });
      }
    }
    placing.current = false;
    setBusy(false);
    setProgress("");
    setCart(() => []);
    onPurchased(placed);
  }

  if (!cart.length) return <section className="stack"><h1>Your cart</h1>
    <p className="empty">Your cart is empty.</p><div><button onClick={onShop}>Start shopping</button></div></section>;

  return <section className="stack"><h1>Your cart</h1><Feedback query={sales} />
    <div className="cart-layout">
      <div className="panel cart-lines">{lines.map(({ line, sale }) => {
        const product = sale && catalog.get(sale.terms.product_id);
        const status = sale ? statusOf(sale, levels.get(sale.sale_id)) : "Ended";
        const level = levels.get(line.sale_id);
        return <article className="cart-line" key={line.sale_id}>
          <ProductImage product={product} small />
          <div className="cart-info"><h3>{product?.name || sale?.terms.product_id || "Unavailable sale"}</h3>
            {sale && <p className="small">{money(sale.terms)} each{product?.regular_price && discount(sale.terms, product.regular_price) > 0 &&
              <> · <s>{formatMoney(product.regular_price.amount_minor, product.regular_price.currency)}</s> <span className="off">{discount(sale.terms, product.regular_price)}% off</span></>}</p>}
            {sale && <StockBar stock={level} limit={sale.terms.stock} />}
            {status !== "Live" && <p className="error">This sale is no longer live. Remove it to check out.</p>}
          </div>
          <div className="quantity" aria-label={`Quantity for ${product?.name || "item"}`}>
            <button className="secondary" aria-label="Decrease quantity" disabled={busy} onClick={() => setQuantity(line.sale_id, line.quantity - 1)}>−</button>
            <span>{line.quantity}</span>
            <button className="secondary" aria-label="Increase quantity" disabled={busy ||  (level !== undefined && line.quantity >= level.available)}
              onClick={() => setQuantity(line.sale_id, line.quantity + 1)}>+</button>
          </div>
          <strong className="line-total">{sale ? formatMoney(sale.terms.price_minor * line.quantity, sale.terms.currency) : "—"}</strong>
          <button className="link" disabled={busy} onClick={() => setQuantity(line.sale_id, 0)}>Remove</button>
        </article>;
      })}</div>
      <aside className="panel summary">
        <h2>Checkout</h2>
        <p className="small">{units} item{units === 1 ? "" : "s"}. Stock is not held until you place the order.</p>
        <fieldset className="payment-choice"><legend>Payment method</legend>
          <label className="radio"><input type="radio" name="payment" checked={payment === "card"} onChange={() => setPayment("card")} />
            <span><strong>Card</strong> · demo card, payment goes through</span></label>
          <label className="radio"><input type="radio" name="payment" checked={payment === "decline"} onChange={() => setPayment("decline")} />
            <span><strong>Test card</strong> · always declined, to see a failed payment</span></label>
        </fieldset>
        {saved > 0 && currencies.size === 1 && <div className="saved"><span>You save</span><strong>{formatMoney(saved, [...currencies][0]!)}</strong></div>}
        <div className="total"><span>Total</span><strong>{currencies.size === 1 ? formatMoney(total, [...currencies][0]!) : "Mixed currencies"}</strong></div>
        <button disabled={busy || unavailable || !units} onClick={() => void checkout()}>{busy ? "Placing order…" : "Place order"}</button>
        {progress && <p role="status">{progress}</p>}
      </aside>
    </div>
  </section>;
}
