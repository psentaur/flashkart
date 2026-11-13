import { useRef, useState } from "react";
import type { FormEvent } from "react";
import { ApiError, api, message, operationID, segment } from "./api";
import type { Context, Draft, Product, ProductView, Publication, Replica, Sale, Terms, Version } from "./api";
import { SaleTerms } from "./customer";
import { useQuery } from "./hooks";
import { Feedback } from "./ui";

export function Admin({ token }: { token: string }) {
  if (!token) return <section><h1>Administration</h1><p className="empty">Add your administrator token to manage the catalog and sales.</p></section>;
  return <div className="stack"><h1>Administration</h1><ProductEditor token={token} /><SaleEditor token={token} /></div>;
}

function ProductEditor({ token }: { token: string }) {
  const [id, setID] = useState("");
  const [product, setProduct] = useState<Product>();
  const [context, setContext] = useState<Context>({});
  const [siblings, setSiblings] = useState<Version[]>([]);
  const [notice, setNotice] = useState("");
  const [error, setError] = useState<unknown>();
  const [busy, setBusy] = useState(false);
  const mutation = useRef<{ signature: string; id: string } | undefined>(undefined);

  function conflict(err: unknown) {
    if (!(err instanceof ApiError) || err.code !== "CATALOG_CONFLICT" || !err.context || !err.siblings?.length) return;
    setContext(err.context);
    setSiblings(err.siblings);
    setProduct(undefined);
  }

  async function load(e: FormEvent) {
    e.preventDefault();
    const productID = id.trim();
    setBusy(true);
    setProduct(undefined);
    setSiblings([]);
    setNotice("");
    setError(undefined);
    mutation.current = undefined;
    try {
      const value = await api<ProductView>("/api/admin/products/" + segment(productID), { token });
      setProduct(value.product);
      setContext(value.context);
    } catch (err) {
      if (err instanceof ApiError && err.status === 404) {
        setProduct({ product_id: productID, name: "" });
        setContext({});
        setNotice("New product");
      } else {
        conflict(err);
        setError(err);
      }
    } finally { setBusy(false); }
  }

  async function save(e: FormEvent) {
    e.preventDefault();
    if (!product || busy) return;
    const resolve = siblings.length > 0;
    const signature = JSON.stringify([product, context, resolve]);
    if (mutation.current?.signature !== signature) mutation.current = { signature, id: operationID() };
    setBusy(true);
    setNotice("");
    setError(undefined);
    try {
      const value = await api<ProductView>(`/api/admin/products/${segment(product.product_id)}${resolve ? "/resolve" : ""}`, {
        method: resolve ? "POST" : "PUT", token,
        body: { operation_id: mutation.current.id, product, context },
      });
      setContext(value.context);
      setSiblings([]);
      setNotice(resolve ? "Conflict resolved." : "Product saved.");
      mutation.current = undefined;
    } catch (err) { conflict(err); setError(err); }
    finally { setBusy(false); }
  }

  return <section className="panel">
    <h2>Products</h2>
    <form onSubmit={load} className="inline-form">
      <label>Product ID<input required maxLength={200} value={id} onChange={e => setID(e.target.value)} /></label>
      <button className="secondary" disabled={busy}>Load product</button>
    </form>
    {notice && <p role="status">{notice}</p>}
    {error !== undefined && <p className="error" role="alert">{message(error)}</p>}
    {siblings.length > 0 && <div className="stack">
      <h3>Choose a version to keep</h3>
      <div className="catalog-list">{siblings.map(version => <button key={`${version.replica_id}:${version.counter}`} className="secondary"
        onClick={() => setProduct({ ...version.product })} disabled={busy}>
        {version.product.name} · {version.replica_id}/{version.counter}
      </button>)}</div>
    </div>}
    {product && <form onSubmit={save} className="form-grid">
      <label>Name<input required maxLength={200} value={product.name} onChange={e => setProduct({ ...product, name: e.target.value })} /></label>
      <label>Image URL<input value={product.image_url || ""} placeholder="https://…" onChange={e => setProduct({ ...product, image_url: e.target.value })} /></label>
      <label className="full">Description<textarea value={product.description || ""} onChange={e => setProduct({ ...product, description: e.target.value })} /></label>
      <div className="full"><button disabled={busy}>{siblings.length ? "Resolve conflict" : "Save product"}</button></div>
    </form>}
  </section>;
}

const localTime = (time: number) => {
  const value = new Date(time);
  return new Date(time - value.getTimezoneOffset() * 60000).toISOString().slice(0, 16);
};

function SaleEditor({ token }: { token: string }) {
  const [draft, setDraft] = useState<Draft>();
  const [terms, setTerms] = useState<Terms>(() => ({
    product_id: "", price_minor: 0, currency: "USD", stock: 1,
    starts_at: localTime(Date.now() - 60_000), ends_at: localTime(Date.now() + 3600_000),
  }));
  const [publication, setPublication] = useState<Publication>();
  const [error, setError] = useState<unknown>();
  const [busy, setBusy] = useState(false);
  const [dirty, setDirty] = useState(false);
  const [operation, setOperation] = useState<string>();
  const progress = useQuery<Publication>(publication ? "/api/admin/publications/" + segment(publication.operation_id) : null, token, 1400, value => value.state === "PUBLISHED");
  const state = progress.data || publication;
  const published = useQuery<Sale>(state?.state === "PUBLISHED" ? "/api/sales/" + segment(state.sale_id) : null);

  function update<K extends keyof Terms>(key: K, value: Terms[K]) {
    setTerms(previous => ({ ...previous, [key]: value }));
    setDirty(true);
  }

  async function save(e: FormEvent) {
    e.preventDefault();
    if (busy || operation) return;
    setError(undefined);
    setBusy(true);
    try {
      const body = { ...terms, starts_at: new Date(terms.starts_at).toISOString(), ends_at: new Date(terms.ends_at).toISOString() };
      setDraft(await api<Draft>(draft ? "/api/admin/sale-drafts/" + segment(draft.sale_id) : "/api/admin/sale-drafts",
        { method: draft ? "PATCH" : "POST", token, body }));
      setDirty(false);
    } catch (err) { setError(err); }
    finally { setBusy(false); }
  }

  async function publish() {
    if (!draft || busy || (dirty && !operation)) return;
    const id = operation || operationID();
    setOperation(id);
    setError(undefined);
    setBusy(true);
    try {
      setPublication(await api<Publication>(`/api/admin/sale-drafts/${segment(draft.sale_id)}/publish`,
        { method: "POST", token, body: { operation_id: id } }));
    } catch (err) {
      if (!operation && err instanceof ApiError && [400, 401, 403, 404, 409].includes(err.status)) {
        setOperation(undefined);
      }
      setError(err);
    } finally { setBusy(false); }
  }

  return <section className="panel">
    <h2>Sale publication</h2>
    {!state && <form onSubmit={save}>
      <fieldset className="form-grid sale-fields" disabled={busy || !!operation}>
        <label>Sale product ID<input required value={terms.product_id} onChange={e => update("product_id", e.target.value)} /></label>
        <label>Price in minor units<input type="number" min="0" step="1" required value={Number.isNaN(terms.price_minor) ? "" : terms.price_minor} onChange={e => update("price_minor", e.target.valueAsNumber)} /></label>
        <label>Currency<input required pattern="[A-Z]{3}" maxLength={3} value={terms.currency} onChange={e => update("currency", e.target.value.toUpperCase())} /></label>
        <label>Stock limit<input type="number" min="1" max="4294967295" step="1" required value={Number.isNaN(terms.stock) ? "" : terms.stock} onChange={e => update("stock", e.target.valueAsNumber)} /></label>
        <label>Starts at<input required type="datetime-local" value={terms.starts_at} onChange={e => update("starts_at", e.target.value)} /></label>
        <label>Ends at<input required type="datetime-local" value={terms.ends_at} onChange={e => update("ends_at", e.target.value)} /></label>
      </fieldset>
      <div className="catalog-list sale-actions"><button disabled={busy || !!operation}>{draft ? "Save draft changes" : "Create draft"}</button>
        {draft && <button className="secondary" type="button" disabled={busy || (dirty && !operation)} onClick={() => void publish()}>{operation ? "Retry publication" : "Publish sale"}</button>}</div>
    </form>}
    {draft && !state && <p role="status">Draft saved · {draft.sale_id}</p>}
    {error !== undefined && <p role="alert" className="error">{message(error)}</p>}
    {state && <div aria-live="polite"><p><span className={`pill ${state.state === "PUBLISHED" ? "live" : ""}`}>{state.state}</span> <span className="identifier">Operation {state.operation_id}</span></p>
      <Feedback query={progress} /><Feedback query={published} />
      {published.data && <><h3>Published terms</h3><SaleTerms sale={published.data} />
        <button className="secondary" onClick={() => { setDraft(undefined); setPublication(undefined); setOperation(undefined); }}>Create another draft</button></>}
    </div>}
  </section>;
}

export function Diagnostics({ token }: { token: string }) {
  const [input, setInput] = useState("");
  const [id, setID] = useState("");
  const replicas = useQuery<Replica[]>(token ? "/api/admin/replicas" : null, token, 4000);
  const path = id && token ? "/api/admin/replicas/products/" + segment(id) : null;
  const versions = useQuery<Version[]>(path, token, 4000);
  const owners = useQuery<string[]>(path ? path + "/owners" : null, token);
  return <section className="stack"><h1>Replica diagnostics</h1>
    {!token ? <p className="empty">Add your administrator token to inspect replica health.</p> : <>
      <div className="panel"><h2>Replica health</h2><Feedback query={replicas} />
        <div className="replicas">{replicas.data?.map(r => <article key={r.replica_id}>
          <h3>Replica {r.replica_id.toUpperCase()}</h3><span className={`pill ${r.backend_ready ? "live" : ""}`}>{r.backend_ready ? "Ready" : "Unavailable"}</span>
          <p>{r.pending_hints} pending hints</p>
        </article>)}</div></div>
      <div className="panel"><h2>Product versions</h2>
        <form className="inline-form" onSubmit={e => { e.preventDefault(); if (id === input.trim()) { versions.reload(); owners.reload(); } else setID(input.trim()); }}>
          <label>Inspect product ID<input required value={input} onChange={e => setInput(e.target.value)} /></label><button>Inspect product</button>
        </form>
        <Feedback query={owners} /><Feedback query={versions} />
        {owners.data && <p>Preferred owners <strong>{owners.data.map(owner => owner.toUpperCase()).join(" · ")}</strong></p>}
        {versions.data?.length === 0 && <p>No visible versions.</p>}
        <div className="catalog-list">{versions.data?.map(version => <article key={`${version.replica_id}:${version.counter}`} className="version">
          <strong>{version.product.name}</strong><p>Replica {version.replica_id.toUpperCase()} · version {version.counter}</p>
          <p>{version.product.description}</p>
        </article>)}</div>
        {(versions.data?.length || 0) > 1 && <p>Open Administration and load this product to resolve its siblings.</p>}
      </div>
    </>}
  </section>;
}
