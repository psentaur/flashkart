import { useEffect, useState } from "react";
import { createRoot } from "react-dom/client";
import { Products, Sales } from "./admin";
import { Analytics } from "./analytics";
import { Bell } from "./bell";
import { Cart } from "./cart";
import { Login } from "./login";
import { Orders } from "./orders";
import { Replicas } from "./replicas";
import { Rush } from "./rush";
import { cartCount, loadCart, loadPurchases, loadSession, saveCart, savePurchases, saveSession } from "./session";
import type { CartLine, Purchase, Session } from "./session";
import { Shop } from "./shop";
import "./style.css";

const customerPages = ["Shop", "Cart", "My orders"] as const;
const adminPages = ["Products", "Sales", "Rush test", "Analytics", "Replicas"] as const;
type Page = (typeof customerPages)[number] | (typeof adminPages)[number];

function App() {
  const [session, setSession] = useState<Session | undefined>(loadSession);
  const [page, setPage] = useState<Page>(() => session?.role === "admin" ? "Products" : "Shop");
  const [cart, setCart] = useState<CartLine[]>(loadCart);
  const [purchases, setPurchases] = useState<Purchase[]>(loadPurchases);
  useEffect(() => saveCart(cart), [cart]);
  useEffect(() => savePurchases(purchases), [purchases]);

  if (!session) return <main className="shell main" id="main">
    <Login onLogin={value => { saveSession(value); setSession(value); setPage(value.role === "admin" ? "Products" : "Shop"); }} />
  </main>;

  const pages: readonly Page[] = session.role === "admin" ? adminPages : customerPages;
  const signOut = () => { saveSession(undefined); setSession(undefined); setCart([]); setPurchases([]); };
  const token = session.token;
  return <>
    <a className="skip" href="#main">Skip to content</a>
    <header className="header"><div className="shell navigation">
      <button className="brand" onClick={() => setPage(pages[0])}><span aria-hidden="true">⚡</span> Flashkart</button>
      <nav aria-label="Main navigation">{pages.map(item => <button key={item}
        aria-current={page === item ? "page" : undefined} onClick={() => setPage(item)}>
        {item}{item === "Cart" && cartCount(cart) > 0 && <span className="count">{cartCount(cart)}</span>}
      </button>)}</nav>
      <div className="account">{session.role === "customer" && <Bell token={token} purchases={purchases} onOpenOrders={() => setPage("My orders")} />}
        <span className="pill role-pill">{session.role === "admin" ? "Administrator" : "Customer"}</span>
        <button className="secondary" onClick={signOut}>Sign out</button></div>
    </div></header>
    <main className="shell main" id="main">
      {page === "Shop" && <Shop cart={cart} setCart={setCart} onCart={() => setPage("Cart")} />}
      {page === "Cart" && <Cart token={token} cart={cart} setCart={setCart}
        onPurchased={added => { setPurchases(previous => [...added, ...previous]); setPage("My orders"); }} onShop={() => setPage("Shop")} />}
      {page === "My orders" && <Orders token={token} purchases={purchases} onShop={() => setPage("Shop")} />}
      {page === "Products" && <Products token={token} />}
      {page === "Sales" && <Sales token={token} />}
      {page === "Rush test" && <Rush />}
      {page === "Analytics" && <Analytics />}
      {page === "Replicas" && <Replicas token={token} />}
    </main>
  </>;
}

const root = document.getElementById("root");
if (!root) throw new Error("Missing Flashkart root element");
createRoot(root).render(<App />);
