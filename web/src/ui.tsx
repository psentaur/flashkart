import { message } from "./api";

export function Feedback({ query }: { query: { loading: boolean; error?: unknown; reload: () => void } }) {
  return <>
    {query.loading && <p role="status">Loading…</p>}
    {query.error !== undefined && <div className="error" role="alert">
      <span>{message(query.error)}</span><button className="secondary" onClick={query.reload}>Retry</button>
    </div>}
  </>;
}
