import { ApiError, api, message, operationID } from "./api";
import type { Attempt, Sale } from "./api";
import type { PaymentChoice, Purchase } from "./session";

const pause = (ms: number) => new Promise(resolve => window.setTimeout(resolve, ms));

/**
 * Submits one purchase attempt for one unit through the Admission Controller.
 * Retries reuse the same attempt ID (the Idempotency-Key), so a lost response never buys twice.
 * A declined-card choice prefixes the ID, which the Payment Service treats like a test card.
 */
export async function submitAttempt(token: string, sale: Sale, payment: PaymentChoice, maxThrottled = 3): Promise<Purchase> {
  const id = (payment === "decline" ? "decline-" : "") + operationID();
  const purchase: Purchase = {
    attempt_id: id, sale_id: sale.sale_id, product_id: sale.terms.product_id,
    created_at: new Date().toISOString(), payment, admission: "failed",
  };
  let throttled = 0;
  for (let tries = 0; tries < 6; tries++) {
    try {
      await api<Attempt>("/api/attempts", { method: "POST", token, attemptID: id, body: { attempt_id: id, sale_id: sale.sale_id } });
      purchase.admission = "admitted";
      purchase.error = undefined;
      return purchase;
    } catch (err) {
      purchase.error = message(err);
      if (err instanceof ApiError && err.status === 429) {
        // The Admission Controller is shedding load; back off as it asks.
        purchase.admission = "throttled";
        if (++throttled > maxThrottled) return purchase;
        await pause(err.retryAfter * 1000);
      } else if (err instanceof ApiError && err.status < 500) {
        return purchase;
      } else {
        await pause(700);
      }
    }
  }
  return purchase;
}
