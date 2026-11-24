export type Credential = string | { userID: string; getToken: () => Promise<string | null> };
export const credentialKey = (token?: Credential) => typeof token === "string" ? token : token?.userID;

export type Price = { amount_minor: number; currency: string };
export type Product = { product_id: string; name: string; description?: string; image_url?: string; regular_price?: Price };
export type Context = Record<string, { first: string; last: string }[]>;
export type ProductView = { product: Product; context: Context };
export type Version = { product: Product; replica_id: string; counter: string };
export type Terms = { product_id: string; price_minor: number; currency: string; stock: number; starts_at: string; ends_at: string };
export type Sale = { sale_id: string; terms: Terms };
export type Draft = Sale & { state: "DRAFT" };
export type Publication = { sale_id: string; operation_id: string; state: "PENDING" | "PUBLISHED" };
export type Attempt = { attempt_id: string; sale_id: string; state: "QUEUED" | "CONFIRMED" | "CANCELLED" | "REJECTED"; reason?: string; order_id?: string };
export type Order = {
  order_id: string; attempt_id: string; sale_id: string; state: "PENDING" | "CONFIRMED" | "CANCELLED";
  amount_minor: number; currency: string; created_at: string; reason?: string;
};
export type Payment = {
  payment_id: string; order_id: string; state: "PENDING" | "SUCCEEDED" | "DECLINED" | "REFUNDED";
  amount_minor: number; currency: string; simulated_decline: boolean;
};
export type Stock = { sale_id: string; stock: number; available: number; held: number; sold: number; active: boolean };
export type Notification = { event_id: string; attempt: Attempt };
export type Replica = { replica_id: string; backend_ready: boolean; pending_hints: number };
export type Window = {
  sale_id: string; starts_at: string; ends_at: string; demand: number; admission_rejections: number;
  stock_rejections: number; reservations: number; confirmed_purchases: number; average_latency_ms: number | null;
};

export class ApiError extends Error {
  constructor(readonly status: number, readonly code: string, message: string,
    readonly retryAfter: number, readonly context?: Context, readonly siblings?: Version[]) {
    super(message);
  }
}

export const segment = encodeURIComponent;
export const operationID = () => Array.from(crypto.getRandomValues(new Uint8Array(16)), byte => byte.toString(16).padStart(2, "0")).join("");

type Options = { method?: string; token?: Credential; body?: unknown; raw?: Blob; attemptID?: string; signal?: AbortSignal };

export async function api<T>(path: string, options: Options = {}): Promise<T> {
  const headers = new Headers();
  if (options.token) {
    const token = typeof options.token === "string" ? options.token : await options.token.getToken();
    if (!token) throw new ApiError(401, "UNAUTHENTICATED", "Sign in to continue.", 1);
    headers.set("Authorization", `Bearer ${token}`);
  }
  if (options.body !== undefined) headers.set("Content-Type", "application/json");
  if (options.raw) headers.set("Content-Type", options.raw.type || "application/octet-stream");
  if (options.attemptID) headers.set("Idempotency-Key", options.attemptID);
  const response = await fetch(path, {
    method: options.method,
    signal: options.signal,
    headers,
    body: options.raw ?? (options.body === undefined ? undefined : JSON.stringify(options.body)),
  });
  const data = await response.json().catch(() => null);
  if (!response.ok || data === null) {
    throw new ApiError(response.status, data?.code || "UNAVAILABLE",
      data?.message || "Service unavailable. Try again.", Math.max(1, Number(response.headers.get("Retry-After")) || 1),
      data?.context, data?.siblings);
  }
  return data as T;
}

export function message(error: unknown) {
  if (error instanceof ApiError) return error.status === 429
    ? `${error.message} Retry in ${error.retryAfter}s.`
    : `${error.message} (${error.code})`;
  return "The request could not be completed. Try again.";
}

const digits = (currency: string) =>
  new Intl.NumberFormat(undefined, { style: "currency", currency }).resolvedOptions().maximumFractionDigits ?? 2;

export function formatMoney(minor: number, currency: string) {
  return new Intl.NumberFormat(undefined, { style: "currency", currency }).format(minor / 10 ** digits(currency));
}

export const money = (terms: Terms) => formatMoney(terms.price_minor, terms.currency);
/** Percentage saved by the flash price against the regular price, when both use the same currency. */
export function discount(terms: Pick<Terms, "price_minor" | "currency">, regular?: Price) {
  if (!regular || regular.currency !== terms.currency || regular.amount_minor <= terms.price_minor) return 0;
  return Math.round((1 - terms.price_minor / regular.amount_minor) * 100);
}

/**
 * A sale is a flash deal when it is cheaper than the product's regular price (or the product has none).
 * A sale at the regular price is a regular listing: the product is simply for sale, through the same services.
 */
export const isFlash = (terms: Terms, product?: Product) => !product?.regular_price || discount(terms, product.regular_price) > 0;

export const fromMinor = (minor: number, currency: string) => minor / 10 ** digits(currency);
export const toMinor = (major: number, currency: string) => Math.round(major * 10 ** digits(currency));

export function date(value: string) {
  return new Date(value).toLocaleString(undefined, { dateStyle: "medium", timeStyle: "short" });
}

export function time(value: string) {
  return new Date(value).toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit" });
}

export type SaleStatus = "Upcoming" | "Live" | "Ended";
export function saleStatus(terms: Terms, now = Date.now()): SaleStatus {
  return now >= Date.parse(terms.ends_at) ? "Ended" : now < Date.parse(terms.starts_at) ? "Upcoming" : "Live";
}

/**
 * Status that also honours an administrator ending the sale early: Inventory Reservation then reports
 * the sale as inactive, whatever its published window says.
 */
export function statusOf(sale: { terms: Terms }, stock?: Stock, now = Date.now()): SaleStatus {
  const status = saleStatus(sale.terms, now);
  return stock && !stock.active && status !== "Ended" ? "Ended" : status;
}

/** Human-readable explanation for a terminal attempt reason. */
export function reasonText(reason?: string) {
  return ({
    SOLD_OUT: "Sold out — no units were left",
    ENDED: "The sale had already ended",
    INACTIVE: "The sale was not active yet",
    THROTTLED: "Throttled during overload",
    PAYMENT_DECLINED: "Card payment was declined",
    RESERVATION_EXPIRED: "Reservation expired before payment completed",
    RESERVATION_RELEASED: "Reservation was released",
  } as Record<string, string>)[reason || ""] || (reason ? reason.replaceAll("_", " ").toLowerCase() : "");
}
