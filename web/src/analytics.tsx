import { useState } from "react";
import { date, segment } from "./api";
import type { Sale, Window } from "./api";
import { useQuery } from "./hooks";
import { Feedback } from "./ui";

const series = [
  ["demand", "Demand"], ["admission_rejections", "Admission rejections"], ["stock_rejections", "Stock rejections"],
  ["reservations", "Reservations"], ["confirmed_purchases", "Confirmed purchases"], ["average_latency_ms", "Average latency (ms)"],
] as const;

export function Analytics() {
  const sales = useQuery<Sale[]>("/api/sales");
  const [selection, setSelection] = useState("");
  const windows = useQuery<Window[]>(selection ? "/api/analytics/sales/" + segment(selection) : null, undefined, 5000);
  const data = windows.data || [];
  const recent = data.slice(-12);
  const last = data.at(-1);
  return <section className="stack"><div><h1>Sales analytics</h1><p>Finalized one-minute windows from the purchase event stream.</p></div>
    <Feedback query={sales} />
    <label className="sale-picker">Analytics sale<select value={selection} onChange={e => setSelection(e.target.value)}>
      <option value="">Choose a published sale</option>{sales.data?.map(s => <option key={s.sale_id} value={s.sale_id}>{s.terms.product_id} · {s.sale_id}</option>)}
    </select></label>
    <Feedback query={windows} />
    {selection && windows.data && <p role="status">
      {last ? `Latest finalized window ended ${date(last.ends_at)}` : "No finalized windows yet."}
      {windows.fetchedAt && ` · Checked ${date(windows.fetchedAt)}`}
    </p>}
    {recent.length > 0 && <><div className="charts">{series.map(([field, label]) => {
      const max = Math.max(1, ...recent.map(w => w[field] || 0));
      return <article className="panel" key={field}><h2>{label}</h2>
        <div className="bars" role="img" aria-label={`${label} across the latest ${recent.length} windows`}>
          {recent.map(w => <div key={w.starts_at} className="bar-track" title={`${date(w.starts_at)} · ${w[field] ?? "No sample"}`}>
            <div className="bar" style={{ height: `${(w[field] || 0) / max * 100}%` }} />
          </div>)}
        </div><p>Latest <strong>{last?.[field] == null ? "—" : Number(last[field].toFixed(2))}</strong></p>
      </article>;
    })}</div>
    <div className="panel table-wrap"><h2>Window history</h2><table><thead><tr><th>Window start</th><th>Window end</th>
      {series.map(([field, label]) => <th key={field}>{label}</th>)}</tr></thead>
      <tbody>{[...data].reverse().map(w => <tr key={w.starts_at}><td>{date(w.starts_at)}</td><td>{date(w.ends_at)}</td>
        {series.map(([field]) => <td key={field}>{w[field] == null ? "—" : Number(w[field].toFixed(2))}</td>)}</tr>)}</tbody></table></div></>}
  </section>;
}
