import http from "k6/http";
import { check } from "k6";
import exec from "k6/execution";

const base = __ENV.BASE_URL;
const headers = { Authorization: "Bearer " + __ENV.FLASHKART_CUSTOMER_TOKEN, "Content-Type": "application/json" };

export const options = {
  scenarios: {
    catalog: { executor: "constant-vus", vus: Number(__ENV.CATALOG_VUS || 30), duration: __ENV.DURATION || "3m", exec: "catalog" },
    admission: { executor: "constant-vus", vus: Number(__ENV.ADMISSION_VUS || 30), duration: __ENV.DURATION || "3m", exec: "admission" },
  },
  thresholds: { checks: ["rate>0.99"] },
};

export function catalog() {
  check(http.get(base + "/api/products"), { "catalog responds": r => r.status === 200 });
}

export function admission() {
  const id = `${__ENV.RUN_ID}-${exec.scenario.iterationInTest}`;
  const requestHeaders = { ...headers, "Idempotency-Key": id };
  const body = JSON.stringify({ attempt_id: id, sale_id: __ENV.SALE_ID });
  const first = http.post(base + "/api/attempts", body, { headers: requestHeaders, responseCallback: http.expectedStatuses(202, 429) });
  check(first, { "attempt accepted or throttled": r => r.status === 202 || r.status === 429 });
  if (first.status === 202) {
    check(http.post(base + "/api/attempts", body, { headers: requestHeaders, responseCallback: http.expectedStatuses(202, 429) }),
      { "idempotent retry accepted or throttled": r => r.status === 202 || r.status === 429 });
  }
}
