import { useEffect, useState } from "react";
import { message } from "./api";
import type { Product, Stock } from "./api";

export function Feedback({ query }: { query: { loading: boolean; error?: unknown; data?: unknown; reload: () => void } }) {
  return <>
    {query.loading && query.data === undefined && <p role="status">Loading…</p>}
    {query.error !== undefined && <div className="error" role="alert">
      <span>{message(query.error)}</span><button className="secondary" onClick={query.reload}>Retry</button>
    </div>}
  </>;
}

export function useNow(interval = 1000) {
  const [now, setNow] = useState(Date.now);
  useEffect(() => {
    const timer = window.setInterval(() => setNow(Date.now()), interval);
    return () => window.clearInterval(timer);
  }, [interval]);
  return now;
}

/** Only images the browser can load: never a stored s3:// reference. */
const displayable = (src?: string) => src && !src.startsWith("s3://") ? src : undefined;

/** Product photo, or a coloured placeholder with initials when there is no image or it fails to load. */
export function ProductImage({ product, small, src: override }: { product?: Product; small?: boolean; src?: string }) {
  const [failed, setFailed] = useState<string>();
  const src = override || displayable(product?.image_url);
  const className = small ? "thumb" : "photo";
  if (src && failed !== src) return <img className={className} src={src} alt={product?.name || ""} loading="lazy" onError={() => setFailed(src)} />;
  const name = product?.name || "?";
  const initials = name.split(/\s+/).map(word => word[0]).join("").slice(0, 2).toUpperCase();
  const hue = [...name].reduce((sum, c) => sum + c.charCodeAt(0), 0) % 360;
  return <div className={`${className} placeholder`} aria-hidden="true"
    style={{ background: `linear-gradient(135deg, hsl(${hue} 80% 92%), hsl(${(hue + 40) % 360} 75% 84%))`, color: `hsl(${hue} 45% 35%)` }}>{initials}</div>;
}

const pad = (n: number) => String(n).padStart(2, "0");
/** "1d 02:05:09" or "02:05:09" until the given time. */
export function countdown(to: string, now: number) {
  const s = Math.max(0, Math.floor((Date.parse(to) - now) / 1000));
  const d = Math.floor(s / 86400), h = Math.floor(s % 86400 / 3600), m = Math.floor(s % 3600 / 60);
  return `${d ? d + "d " : ""}${pad(h)}:${pad(m)}:${pad(s % 60)}`;
}

/** Live "X of Y left" from Inventory Reservation, turning urgent as stock runs out. */
export function StockBar({ stock, limit, detail }: { stock?: Stock; limit: number; detail?: boolean }) {
  if (!stock) return <p className="small">{limit} units in this sale</p>;
  const left = Math.max(0, stock.available);
  const share = stock.stock ? left / stock.stock : 0;
  const level = left === 0 ? "out" : share <= .25 ? "low" : "ok";
  return <div className={`stock ${level}`} aria-label={`${left} of ${stock.stock} left`}>
    <div className="stock-track"><div className="stock-fill" style={{ width: `${(1 - share) * 100}%` }} /></div>
    <span className="small">{left === 0 ? <strong>All {stock.stock} claimed</strong>
      : <><strong>{left}</strong> of {stock.stock} left{level === "low" && " · selling fast"}</>}
      {detail && stock.held > 0 && ` · ${stock.held} on hold`}{detail && stock.sold > 0 && ` · ${stock.sold} sold`}</span>
  </div>;
}

export type StepState = "done" | "failed" | "active" | "waiting" | "skipped";
export type Step = { service: string; label: string; state: StepState };

/** The path purchase attempts take through the microservices (administrator view). */
export function Pipeline({ steps }: { steps: Step[] }) {
  return <ol className="pipeline">{steps.map(step => <li key={step.service} className={step.state}>
    <span className="dot" aria-hidden="true">{step.state === "done" ? "✓" : step.state === "failed" ? "✕" : step.state === "active" ? "…" : ""}</span>
    <div><strong>{step.service}</strong><span>{step.label}</span></div>
  </li>)}</ol>;
}

/** Customer-facing order progress in plain words: where the order is, or where it stopped. */
export function Tracker({ steps }: { steps: { label: string; state: StepState }[] }) {
  return <ol className="tracker">{steps.map(step => <li key={step.label} className={step.state}>
    <span className="dot" aria-hidden="true">{step.state === "done" ? "✓" : step.state === "failed" ? "✕" : ""}</span>
    <span>{step.label}</span>
  </li>)}</ol>;
}
