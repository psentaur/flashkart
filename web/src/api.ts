export type Product = { product_id: string; name: string; description?: string; image_url?: string };
export type Context = Record<string, { first: string; last: string }[]>;
export type ProductView = { product: Product; context: Context };
export type Version = { product: Product; replica_id: string; counter: string };
export type Terms = { product_id: string; price_minor: number; currency: string; stock: number; starts_at: string; ends_at: string };
export type Sale = { sale_id: string; terms: Terms };
export type Draft = Sale & { state: "DRAFT" };
export type Publication = { sale_id: string; operation_id: string; state: "PENDING" | "PUBLISHED" };
export type Attempt = { attempt_id: string; sale_id: string; state: "QUEUED" | "CONFIRMED" | "CANCELLED" | "REJECTED"; reason?: string; order_id?: string };
export type Order = { order_id: string; attempt_id: string; state: string };
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

export async function api<T>(path: string, options: { method?: string; token?: string; body?: unknown; attemptID?: string; signal?: AbortSignal } = {}): Promise<T> {
  const headers = new Headers();
  if (options.token) headers.set("Authorization", `Bearer ${options.token}`);
  if (options.body !== undefined) headers.set("Content-Type", "application/json");
  if (options.attemptID) headers.set("Idempotency-Key", options.attemptID);
  const response = await fetch(path, {
    method: options.method,
    signal: options.signal,
    headers,
    body: options.body === undefined ? undefined : JSON.stringify(options.body),
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

export function money(terms: Terms) {
  const formatter = new Intl.NumberFormat(undefined, { style: "currency", currency: terms.currency });
  return formatter.format(terms.price_minor / 10 ** (formatter.resolvedOptions().maximumFractionDigits ?? 2));
}

export function date(value: string) {
  return new Date(value).toLocaleString(undefined, { dateStyle: "medium", timeStyle: "short" });
}
