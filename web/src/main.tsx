import { useEffect, useState } from "react";
import { createRoot } from "react-dom/client";
import { BrowserRouter, Navigate, Route, Routes, useLocation, useNavigate } from "react-router-dom";
import type { ReactNode } from "react";
import { Products, Sales } from "./admin";
import { Analytics } from "./analytics";
import { AccountControls, Authentication, Login, demoMode, useSession } from "./auth";
import { Bell } from "./bell";
import { Cart } from "./cart";
import { Orders } from "./orders";
import { Replicas } from "./replicas";
import { cartCount, loadCart, loadPurchases, saveCart, savePurchases } from "./session";
import { Shop } from "./shop";
import { Team } from "./team";
import { message } from "./api";
import "./style.css";

const pages = [
  ["Shop", "/", "public"], ["Cart", "/activity/cart", "customer"], ["My orders", "/activity", "customer"],
  ["Products", "/admin", "admin"], ["Sales", "/admin/sales", "admin"],
  ["Analytics", "/analytics", "admin"],
  ["Replicas", "/replicas", "admin"], ["Team", "/team", "owner"],
] as const;

function Guard({ role = "customer", children }: { role?: "customer" | "admin" | "owner"; children: ReactNode }) {
  const auth = useSession();
  const location = useLocation();
  if (auth.loading) return <p role="status">Checking your session…</p>;
  if (auth.error !== undefined) return <p role="alert">{message(auth.error)}</p>;
  if (!auth.principal) return <Navigate to={"/login?returnTo=" + encodeURIComponent(location.pathname + location.search)} replace />;
  if (role === "customer") return auth.principal.role === "customer" ? children : <Navigate to="/admin" replace />;
  const allowed = auth.principal.role === "owner" || (role === "admin" && auth.principal.role === "admin");
  if (!allowed) return <section><h1>Access restricted</h1><p>Your account does not have access to this page.</p></section>;
  return children;
}

function App() {
  const { principal } = useSession();
  return <Marketplace key={principal?.user_id || "guest"} userID={principal?.user_id} />;
}

function Marketplace({ userID }: { userID?: string }) {
  const auth = useSession();
  const navigate = useNavigate();
  const location = useLocation();
  const [cart, setCart] = useState(() => userID ? loadCart(userID) : []);
  const [purchases, setPurchases] = useState(() => userID ? loadPurchases(userID) : []);
  useEffect(() => { if (userID) saveCart(userID, cart); }, [userID, cart]);
  useEffect(() => { if (userID) savePurchases(userID, purchases); }, [userID, purchases]);
  const openOrders = (id?: string) => navigate(id ? `/activity?order=${encodeURIComponent(id)}` : "/activity");
  const openCart = () => navigate("/activity/cart");
  const accountRole = auth.principal?.role;
  const staff = accountRole === "admin" || accountRole === "owner";
  const home = staff ? "/admin" : "/";
  const visible = pages.filter(([, , role]) => {
    if (staff) return role === "admin" || (role === "owner" && accountRole === "owner");
    return role === "public" || (role === "customer" && accountRole === "customer");
  });
  return <>
    <a className="skip" href="#main">Skip to content</a>
    <header className="header"><div className="shell navigation">
      <button className="brand" onClick={() => navigate(home)}><span aria-hidden="true">⚡</span> Flashkart</button>
      <nav aria-label="Main navigation">{visible.map(([label, path]) => <button key={path}
        aria-current={location.pathname === path ? "page" : undefined} onClick={() => navigate(path)}>{label}
        {label === "Cart" && cart.length > 0 && <span className="count">{cartCount(cart)}</span>}</button>)}</nav>
      <div className="account">
        {accountRole === "customer" && userID && <Bell key={userID} userID={userID} token={auth.customer} purchases={purchases} onOpenOrder={openOrders} />}
        {auth.principal ? <AccountControls /> : <button className="secondary" onClick={() => navigate("/login")}>Sign in</button>}
        {demoMode && <button className="secondary" onClick={() => navigate("/login")}>Access tokens</button>}
      </div>
    </div></header>
    <main className="shell main" id="main"><Routes>
      <Route path="/" element={staff ? <Navigate to={home} replace /> : <Shop cart={cart} setCart={setCart} onCart={openCart}
        onSignIn={userID ? undefined : () => navigate("/login")} />} />
      <Route path="/login/*" element={<Login />} />
      <Route path="/sign-up/*" element={<Login signUp />} />
      <Route path="/activity/cart" element={<Guard><Cart token={auth.customer} cart={cart} setCart={setCart} onShop={() => navigate("/")}
        onPurchased={added => { setPurchases(previous => [...added, ...previous]); openOrders(); }} /></Guard>} />
      <Route path="/activity" element={<Guard><Orders token={auth.customer} purchases={purchases}
        focus={new URLSearchParams(location.search).get("order") || undefined} onShop={() => navigate("/")} /></Guard>} />
      <Route path="/analytics" element={<Guard role="admin"><Analytics token={auth.admin} /></Guard>} />
      <Route path="/admin" element={<Guard role="admin"><Products token={auth.admin} /></Guard>} />
      <Route path="/admin/sales" element={<Guard role="admin"><Sales token={auth.admin} /></Guard>} />
      <Route path="/replicas" element={<Guard role="admin"><Replicas token={auth.admin} /></Guard>} />
      <Route path="/team" element={<Guard role="owner"><Team token={auth.admin} /></Guard>} />
      <Route path="*" element={<Navigate to={home} replace />} />
    </Routes></main>
  </>;
}

const root = document.getElementById("root");
if (!root) throw new Error("Missing Flashkart root element");
createRoot(root).render(<BrowserRouter><Authentication><App /></Authentication></BrowserRouter>);
