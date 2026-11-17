import type { Sale } from "./api";

export type Role = "customer" | "admin";
export type Session = { role: Role; token: string };
export type CartLine = { sale_id: string; quantity: number };
export type PaymentChoice = "card" | "decline";
/** A purchase attempt sent from this browser. The attempt ID is also the order and payment ID. */
export type Purchase = {
  attempt_id: string; sale_id: string; product_id: string; created_at: string; payment: PaymentChoice;
  admission: "admitted" | "throttled" | "failed"; error?: string;
  /** Units placed together from the cart share a checkout and show as one order. */
  checkout?: string;
  /** Set for attempts sent together by the administrator's rush test; they are summarised as one run. */
  crowd?: string;
};

// Session storage keeps the demo session for this tab only; it is cleared when the tab closes.
function read<T>(key: string, fallback: T): T {
  try {
    const value = sessionStorage.getItem("flashkart." + key);
    return value ? JSON.parse(value) as T : fallback;
  } catch {
    return fallback;
  }
}

function write(key: string, value: unknown) {
  try {
    if (value === undefined) sessionStorage.removeItem("flashkart." + key);
    else sessionStorage.setItem("flashkart." + key, JSON.stringify(value));
  } catch {
    // Storage can be unavailable (private modes); the app keeps working in memory.
  }
}

export const loadSession = () => read<Session | undefined>("session", undefined);
export const saveSession = (session?: Session) => write("session", session);
export const loadCart = () => read<CartLine[]>("cart", []);
export const saveCart = (cart: CartLine[]) => write("cart", cart);
export const loadPurchases = () => read<Purchase[]>("purchases", []);
export const savePurchases = (purchases: Purchase[]) => write("purchases", purchases);

export const cartCount = (cart: CartLine[]) => cart.reduce((sum, line) => sum + line.quantity, 0);
export const saleByID = (sales: Sale[] | undefined) => new Map((sales || []).map(sale => [sale.sale_id, sale]));
