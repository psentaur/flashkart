import type { Notification, Sale } from "./api";
export type CartLine = { sale_id: string; quantity: number };
export type PaymentChoice = "card" | "decline";
/** A purchase attempt sent from this browser. The attempt ID is also the order and payment ID. */
export type Purchase = {
  attempt_id: string; sale_id: string; product_id?: string; created_at?: string; payment?: PaymentChoice;
  admission: "admitted" | "throttled" | "failed"; error?: string;
  /** Units placed together from the cart share a checkout and show as one order. */
  checkout?: string;
  /** Set for attempts sent together by the administrator's rush test; they are summarised as one run. */
  crowd?: string;
};

// Keep cart and checkout groupings separate for each account on this browser tab.
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

export const loadCart = (userID: string) => read<CartLine[]>(`cart.${userID}`, []);
export const saveCart = (userID: string, cart: CartLine[]) => write(`cart.${userID}`, cart);
export const loadPurchases = (userID: string) => read<Purchase[]>(`purchases.${userID}`, []);
export const savePurchases = (userID: string, purchases: Purchase[]) => write(`purchases.${userID}`, purchases);

export function orderItems(purchases: Purchase[], notifications: Notification[] = []): Purchase[] {
  const known = new Set(purchases.map(purchase => purchase.attempt_id));
  const previous = notifications.flatMap(({ attempt }) => known.has(attempt.attempt_id) ? [] : [{
    attempt_id: attempt.attempt_id, sale_id: attempt.sale_id, admission: "admitted" as const,
  }]);
  return [...purchases.filter(purchase => !purchase.crowd), ...previous];
}

export const cartCount = (cart: CartLine[]) => cart.reduce((sum, line) => sum + line.quantity, 0);
export const saleByID = (sales: Sale[] | undefined) => new Map((sales || []).map(sale => [sale.sale_id, sale]));
