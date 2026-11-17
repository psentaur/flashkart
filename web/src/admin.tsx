import { useEffect, useRef, useState } from "react";
import type { FormEvent } from "react";
import { ApiError, api, date, discount, formatMoney, fromMinor, isFlash, message, money, operationID, saleStatus, segment, statusOf, toMinor } from "./api";
import type { Context, Draft, Product, ProductView, Publication, Sale, Stock, Version } from "./api";
import { useQuery } from "./hooks";
import { SortPicker } from "./shop";
import type { SortKey } from "./shop";
import { Feedback, ProductImage, StockBar } from "./ui";

const slug = (value: string) => value.toLowerCase().normalize("NFKD").replace(/[^a-z0-9]+/g, "-").replace(/^-+|-+$/g, "").slice(0, 60);

export function Products({ token }: { token: string }) {
  const products = useQuery<Product[]>("/api/products");
  const sales = useQuery<Sale[]>("/api/sales", undefined, 10_000);
  const stock = useQuery<Stock[]>("/api/inventory/sales", undefined, 4000);
  const [sort, setSort] = useState<SortKey>("sale");
  const levels = new Map(stock.data?.map(item => [item.sale_id, item]));
  // Every product with its listing status; a product that is not on sale has no stock for customers.
  const rows = (products.data || []).map(item => {
    const mine = (sales.data || []).filter(s => s.terms.product_id === item.product_id);
    const live = mine.filter(s => statusOf(s, levels.get(s.sale_id)) === "Live");
    const flash = live.find(s => isFlash(s.terms, item));
    const regular = live.find(s => !isFlash(s.terms, item));
    const upcoming = mine.some(s => statusOf(s, levels.get(s.sale_id)) === "Upcoming");
    const units = live.reduce((sum, s) => sum + Math.max(0, levels.get(s.sale_id)?.available ?? s.terms.stock), 0);
    const price = (flash || regular)?.terms.price_minor ?? item.regular_price?.amount_minor ?? 0;
    return { item, flash, regular, upcoming, units, price, listed: !!(flash || regular) };
  });
  rows.sort((a, b) => sort === "low" ? a.price - b.price : sort === "high" ? b.price - a.price
    : Number(b.listed) - Number(a.listed) || Number(!!b.flash) - Number(!!a.flash) || a.item.name.localeCompare(b.item.name));
  const [editing, setEditing] = useState<{ id?: string; key: number }>();
  // The catalog list refuses to pick a version when replicas hold siblings; offer to resolve that product.
  const conflicted = products.error instanceof ApiError && products.error.code === "CATALOG_CONFLICT"
    ? products.error.siblings?.[0]?.product.product_id : undefined;
  return <div className="stack">
    <div className="section-head"><div><h1>Products</h1>
      <p>The Product Catalog stores each product in the leaderless replication layer across four PostgreSQL replicas.</p></div>
      <button onClick={() => setEditing({ key: Date.now() })}>New product</button></div>
    {editing && <ProductEditor key={editing.key} token={token} productID={editing.id}
      onSaved={() => products.reload()} onClose={() => setEditing(undefined)} />}
    {conflicted ? <div className="notice" role="alert"><h3>Replicas disagree about “{conflicted}”</h3>
      <p>Concurrent edits left more than one version of this product. Resolve it to restore the catalog.</p>
      <button onClick={() => setEditing({ id: conflicted, key: Date.now() })}>Resolve {conflicted}</button></div>
      : <Feedback query={products} />}
    {products.data?.length === 0 && <p className="empty">No products yet. Create the first one.</p>}
    {rows.length > 0 && <div className="toolbar"><p className="small">{rows.filter(r => r.listed).length} of {rows.length} on sale.
      Products that are not listed are hidden from customers — put them on sale from the Sales page.</p>
      <SortPicker value={sort} onChange={setSort} labels={{ sale: "On sale first" }} /></div>}
    <div className="cards compact">{rows.map(({ item, flash, regular, upcoming, units, listed }) => <article className={`product-card ${listed ? "" : "muted"}`} key={item.product_id}>
      <ProductImage product={item} />
      <div className="card-content"><h3>{item.name}</h3><p className="identifier">{item.product_id}</p>
        {item.description && <p className="clamp">{item.description}</p>}
        <p className="small">{item.regular_price ? <>Regular price {formatMoney(item.regular_price.amount_minor, item.regular_price.currency)}</> : "No regular price"}</p>
        <div className="tags">
          {flash && <span className="pill flash">⚡ Flash · {money(flash.terms)}</span>}
          {regular && <span className="pill live">Regular · {money(regular.terms)}</span>}
          {!listed && <span className="pill">{upcoming ? "Sale scheduled" : "Not listed"}</span>}
        </div>
        <p className="small">Stock for sale: <strong>{units}</strong></p>
        <button className="secondary" onClick={() => setEditing({ id: item.product_id, key: Date.now() })}>Edit</button></div>
    </article>)}</div>
  </div>;
}

function ProductEditor({ token, productID, onSaved, onClose }: { token: string; productID?: string; onSaved: () => void; onClose: () => void }) {
  const isNew = !productID;
  const [id, setID] = useState(productID || "");
  const [idTouched, setIDTouched] = useState(false);
  const [product, setProduct] = useState<Product>({ product_id: productID || "", name: "" });
  const [preview, setPreview] = useState<string>();
  const [context, setContext] = useState<Context>({});
  const [siblings, setSiblings] = useState<Version[]>([]);
  const [notice, setNotice] = useState("");
  const [error, setError] = useState<unknown>();
  const [busy, setBusy] = useState(false);
  const [file, setFile] = useState<File>();
  const [dragging, setDragging] = useState(false);
  const [regular, setRegular] = useState("");
  const [regularCurrency, setRegularCurrency] = useState("INR");
  const mutation = useRef<{ signature: string; id: string } | undefined>(undefined);
  const loaded = useQuery<ProductView>(productID ? "/api/admin/products/" + segment(productID) : null, token, 0);
  const published = useQuery<ProductView>(productID ? "/api/products/" + segment(productID) : null);
  const [initialized, setInitialized] = useState(isNew);

  useEffect(() => {
    if (initialized || (!loaded.data && loaded.error === undefined)) return;
    setInitialized(true);
    if (loaded.data) {
      setProduct(loaded.data.product); setContext(loaded.data.context);
      const price = loaded.data.product.regular_price;
      if (price) { setRegular(String(fromMinor(price.amount_minor, price.currency))); setRegularCurrency(price.currency); }
    }
    else conflict(loaded.error);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [loaded.data, loaded.error, initialized]);
  // The edit view returns the stored s3:// reference; the public view carries a signed URL to display.
  const publishedImage = published.data?.product?.image_url;
  const shownImage = preview || (product.image_url && product.image_url === loaded.data?.product.image_url ? publishedImage : undefined);

  function conflict(err: unknown) {
    if (!(err instanceof ApiError) || err.code !== "CATALOG_CONFLICT" || !err.context || !err.siblings?.length) return;
    setContext(err.context);
    setSiblings(err.siblings);
    setProduct({ ...err.siblings[0].product });
  }

  // A chosen photo is previewed at once and uploaded when the product is saved.
  function choose(chosen?: File) {
    if (!chosen) return;
    setError(undefined);
    if (!["image/png", "image/jpeg", "image/webp", "image/gif"].includes(chosen.type)) {
      setError(new ApiError(400, "INVALID_IMAGE", "Choose a JPEG, PNG, WebP or GIF image.", 1)); return;
    }
    if (chosen.size > 5 * 1024 * 1024) { setError(new ApiError(413, "IMAGE_TOO_LARGE", "Images must be 5 MB or smaller.", 1)); return; }
    if (preview) URL.revokeObjectURL(preview);
    setFile(chosen);
    setPreview(URL.createObjectURL(chosen));
  }
  function removeImage() {
    if (preview) URL.revokeObjectURL(preview);
    setFile(undefined); setPreview(undefined);
    setProduct(previous => ({ ...previous, image_url: undefined }));
  }

  async function save(e: FormEvent) {
    e.preventDefault();
    if (busy) return;
    const finalID = (isNew ? id : productID)!.trim();
    const resolve = siblings.length > 0;
    setBusy(true);
    setNotice("");
    setError(undefined);
    try {
      if (isNew) {
        // Never overwrite an existing product from the "new" form.
        const existing = await api<Product>("/api/products/" + segment(finalID)).catch(err => err instanceof ApiError && err.status === 404 ? undefined : Promise.reject(err));
        if (existing) throw new ApiError(409, "PRODUCT_EXISTS", `A product with ID "${finalID}" already exists. Choose another ID or edit that product.`, 1);
      }
      const amount = Number(regular.trim());
      if (regular.trim() && !(amount > 0)) throw new ApiError(400, "INVALID_ARGUMENT", "Enter a regular price above zero, or leave it empty.", 1);
      let body: Product = { ...product, product_id: finalID, name: product.name.trim(),
        regular_price: regular.trim() ? { amount_minor: toMinor(amount, regularCurrency), currency: regularCurrency } : undefined };
      if (file) {
        setNotice("Uploading photo…");
        const stored = await api<{ image_url: string }>(`/api/admin/products/${segment(finalID)}/image`, { method: "POST", token, raw: file });
        body = { ...body, image_url: stored.image_url };
        setProduct(body);
        setFile(undefined);
      }
      const signature = JSON.stringify([body, context, resolve]);
      if (mutation.current?.signature !== signature) mutation.current = { signature, id: operationID() };
      const value = await api<ProductView>(`/api/admin/products/${segment(finalID)}${resolve ? "/resolve" : ""}`, {
        method: resolve ? "POST" : "PUT", token, body: { operation_id: mutation.current.id, product: body, context },
      });
      setContext(value.context);
      setSiblings([]);
      mutation.current = undefined;
      onSaved();
      onClose();
    } catch (err) { conflict(err); setError(err); setNotice(""); }
    finally { setBusy(false); }
  }

  return <section className="panel" aria-label="Product editor">
    <div className="section-head"><h2>{isNew ? "New product" : `Edit ${product.name || productID}`}</h2>
      <button className="secondary" onClick={onClose}>Cancel</button></div>
    {!initialized && <p role="status">Loading product…</p>}
    {siblings.length > 0 && <div className="notice"><h3>Replicas disagree about this product</h3>
      <p>Concurrent edits produced {siblings.length} versions. Pick the one to keep, adjust it, then resolve.</p>
      <div className="catalog-list">{siblings.map(version => <button key={`${version.replica_id}:${version.counter}`} className="secondary"
        onClick={() => setProduct({ ...version.product })} disabled={busy}>{version.product.name} · replica {version.replica_id.toUpperCase()}</button>)}</div>
    </div>}
    {initialized && <form onSubmit={save} className="editor">
      <div className="editor-image">
        {shownImage ? <>
          <img className="photo" src={shownImage} alt="Product photo" />
          <div className="image-actions">
            <label className="upload small-upload">Change photo
              <input type="file" accept="image/png,image/jpeg,image/webp,image/gif" disabled={busy} onChange={e => { choose(e.target.files?.[0]); e.target.value = ""; }} /></label>
            <button type="button" className="link" disabled={busy} onClick={removeImage}>Remove</button>
          </div>
          {file && <p className="small">New photo — it uploads when you {isNew ? "create" : "save"} the product.</p>}
        </> : <label className={`dropzone ${dragging ? "over" : ""}`}
          onDragOver={e => { e.preventDefault(); setDragging(true); }} onDragLeave={() => setDragging(false)}
          onDrop={e => { e.preventDefault(); setDragging(false); choose(e.dataTransfer.files[0]); }}>
          <span className="dropzone-icon" aria-hidden="true">📷</span>
          <strong>Upload product photo</strong>
          <span className="small">Click to choose, or drag an image here</span>
          <input type="file" accept="image/png,image/jpeg,image/webp,image/gif" disabled={busy} onChange={e => { choose(e.target.files?.[0]); e.target.value = ""; }} />
        </label>}
        <p className="small">JPEG, PNG, WebP or GIF, up to 5 MB.</p>
      </div>
      <div className="form-grid">
        <label className="full">Name<input required maxLength={200} value={product.name} onChange={e => {
          const name = e.target.value;
          setProduct({ ...product, name });
          if (isNew && !idTouched) setID(slug(name));
        }} /></label>
        <label className="full">Product ID{isNew
          ? <input required maxLength={200} pattern="[A-Za-z0-9][A-Za-z0-9._-]*" title="Letters, numbers, dot, dash or underscore" value={id}
            onChange={e => { setID(e.target.value); setIDTouched(true); }} />
          : <input value={productID} disabled />}
          <span className="small">{isNew ? "A short, permanent identifier. Sales refer to the product by this ID." : "The ID cannot be changed."}</span></label>
        <label className="full">Description<textarea value={product.description || ""} onChange={e => setProduct({ ...product, description: e.target.value })} /></label>
        <label>Regular price <span className="small">(optional)</span><input inputMode="decimal" placeholder="999.00" value={regular}
          onChange={e => setRegular(e.target.value)} />
          <span className="small">Everyday price. Flash sales show their discount against it.</span></label>
        <label>Currency<select value={regularCurrency} onChange={e => setRegularCurrency(e.target.value)}>
          {["INR", "USD", "EUR"].map(c => <option key={c}>{c}</option>)}</select></label>
        <div className="full"><button disabled={busy}>{siblings.length ? "Resolve conflict" : isNew ? "Create product" : "Save changes"}</button></div>
      </div>
    </form>}
    {notice && <p role="status">{notice}</p>}
    {error !== undefined && <p className="error" role="alert">{message(error)}</p>}
  </section>;
}

const localTime = (time: number) => {
  const value = new Date(time);
  return new Date(time - value.getTimezoneOffset() * 60000).toISOString().slice(0, 16);
};

export function Sales({ token }: { token: string }) {
  const products = useQuery<Product[]>("/api/products");
  const sales = useQuery<Sale[]>("/api/sales", undefined, 10_000);
  const stock = useQuery<Stock[]>("/api/inventory/sales", undefined, 4000);
  const catalog = new Map(products.data?.map(item => [item.product_id, item]));
  const levels = new Map(stock.data?.map(item => [item.sale_id, item]));
  const [form, setForm] = useState(Date.now());
  const [prefill, setPrefill] = useState<Prefill>();
  const [confirming, setConfirming] = useState<string>();
  const [ending, setEnding] = useState<string>();
  const [ended, setEnded] = useState<Sale>();
  const [endError, setEndError] = useState<unknown>();
  const top = useRef<HTMLDivElement>(null);
  const notice = useRef<HTMLDivElement>(null);
  // The End button disappears once the sale ends, so move focus to the confirmation instead of losing it.
  useEffect(() => { if (ended) notice.current?.focus(); }, [ended]);

  async function end(sale: Sale) {
    if (ending) return;
    setEnding(sale.sale_id);
    setEndError(undefined);
    try {
      await api<Stock>(`/api/inventory/sales/${segment(sale.sale_id)}/end`, { method: "POST", token });
      setEnded(sale);
    } catch (err) {
      // Already ended (for example by an earlier click whose response was lost): treat as done.
      if (err instanceof ApiError && err.code === "SALE_NOT_ACTIVE") setEnded(sale); else setEndError(err);
    } finally {
      setEnding(undefined); setConfirming(undefined); stock.reload();
    }
  }
  function replace(sale: Sale) {
    setPrefill({ product_id: sale.terms.product_id, kind: isFlash(sale.terms, catalog.get(sale.terms.product_id)) ? "flash" : "regular",
      stock: sale.terms.stock });
    setEnded(undefined);
    setForm(Date.now());
    top.current?.scrollIntoView({ behavior: "smooth" });
  }
  const list = [...(sales.data || [])].sort((a, b) => b.terms.starts_at.localeCompare(a.terms.starts_at));
  return <div className="stack" ref={top}>
    <div><h1>Sales</h1><p>Put products up for sale: as a short, discounted flash sale, or as a regular listing at the regular price. The Flash Sale Manager publishes both; Inventory Reservation enforces their stock.</p></div>
    <SaleEditor key={form} token={token} products={products.data || []} initial={prefill}
      onPublished={() => { sales.reload(); stock.reload(); products.reload(); }} onReset={() => { setPrefill(undefined); setForm(Date.now()); }}
      onProductChanged={() => products.reload()} />
    <section className="panel table-wrap"><h2>Published</h2><Feedback query={sales} />
      {ended && <div className="notice" role="status" tabIndex={-1} ref={notice}><strong>{catalog.get(ended.terms.product_id)?.name || "The sale"} has ended.</strong> New orders are refused;
        orders already in progress finish normally. To change its price or stock, publish a replacement.
        <div className="catalog-list"><button onClick={() => replace(ended)}>Publish a replacement</button>
          <button className="secondary" onClick={() => setEnded(undefined)}>Done</button></div></div>}
      {endError !== undefined && <p role="alert" className="error">{message(endError)}</p>}
      {list.length === 0 && <p className="empty">No sales published yet.</p>}
      {list.length > 0 && <table><thead><tr><th>Product</th><th>Type</th><th>Price</th><th>Window</th><th>Status</th><th>Stock</th><th><span className="sr-only">Actions</span></th></tr></thead>
        <tbody>{list.map(sale => {
          const product = catalog.get(sale.terms.product_id);
          const status = statusOf(sale, levels.get(sale.sale_id));
          const name = product?.name || sale.terms.product_id;
          return <tr key={sale.sale_id}>
            <td><div className="cell-product"><ProductImage product={product} small /><span>{product?.name || sale.terms.product_id}</span></div></td>
            <td>{isFlash(sale.terms, product) ? <span className="pill flash">⚡ Flash sale</span> : <span className="pill">Regular</span>}</td>
            <td>{money(sale.terms)}</td><td>{date(sale.terms.starts_at)} – {date(sale.terms.ends_at)}</td>
            <td><span className={`pill ${status === "Live" ? "live" : ""}`}>{status}</span></td>
            <td className="stock-cell">{status === "Ended" && levels.get(sale.sale_id)
              ? <span className="small">{levels.get(sale.sale_id)!.sold} sold · {Math.max(0, levels.get(sale.sale_id)!.available)} unsold</span>
              : <StockBar stock={levels.get(sale.sale_id)} limit={sale.terms.stock} />}</td>
            <td className="actions">{status !== "Ended" && levels.get(sale.sale_id)?.active && (confirming === sale.sale_id
              ? <span className="confirm"><span className="small">End now?</span>
                <button className="danger" disabled={!!ending} onClick={() => void end(sale)}>{ending === sale.sale_id ? "Ending…" : "End"}</button>
                <button className="secondary" disabled={!!ending} onClick={() => setConfirming(undefined)}>Keep</button></span>
              : <button className="secondary" onClick={() => setConfirming(sale.sale_id)} title={`End the sale of ${name}`}>End sale</button>)}</td>
          </tr>;
        })}</tbody></table>}
    </section>
  </div>;
}

type Form = { product_id: string; price: string; currency: string; stock: number; starts_at: string; ends_at: string };
type Prefill = { product_id: string; kind: "flash" | "regular"; stock: number };

function SaleEditor({ token, products, initial, onPublished, onReset, onProductChanged }: {
  token: string; products: Product[]; initial?: Prefill; onPublished: () => void; onReset: () => void; onProductChanged: () => void;
}) {
  const [draft, setDraft] = useState<Draft>();
  const [form, setForm] = useState<Form>(() => ({
    product_id: initial?.product_id || "", price: "", currency: "INR", stock: initial?.stock || 10,
    starts_at: localTime(Date.now() - 60_000), ends_at: localTime(Date.now() + 3600_000),
  }));
  const [publication, setPublication] = useState<Publication>();
  const [error, setError] = useState<unknown>();
  const [busy, setBusy] = useState(false);
  const [dirty, setDirty] = useState(false);
  const [operation, setOperation] = useState<string>();
  const progress = useQuery<Publication>(publication ? "/api/admin/publications/" + segment(publication.operation_id) : null, token, 1000, value => value.state === "PUBLISHED");
  const state = progress.data || publication;
  const product = products.find(item => item.product_id === form.product_id);
  const [kind, setKind] = useState<"flash" | "regular">("flash");
  // A replacement starts from the ended sale's product and type.
  const prefilled = useRef(false);
  useEffect(() => {
    if (prefilled.current || !initial || !product) return;
    prefilled.current = true;
    if (initial.kind === "regular") choose("regular", product);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [initial, product]);
  const regular = kind === "regular";

  // A regular listing sells at the product's regular price for a long window.
  function choose(next: "flash" | "regular", item = product) {
    setKind(next);
    setDirty(true);
    if (next === "regular") {
      const price = item?.regular_price;
      setForm(previous => ({ ...previous, ends_at: localTime(Date.now() + 30 * 86400_000),
        ...(price ? { price: String(fromMinor(price.amount_minor, price.currency)), currency: price.currency } : {}) }));
    } else if (next === "flash") {
      setForm(previous => ({ ...previous, price: "", ends_at: localTime(Date.now() + 3600_000) }));
    }
  }

  function update<K extends keyof Form>(key: K, value: Form[K]) {
    setForm(previous => ({ ...previous, [key]: value }));
    setDirty(true);
  }

  async function save(e: FormEvent) {
    e.preventDefault();
    if (busy || operation) return;
    setError(undefined);
    const price = Number(form.price);
    if (!form.product_id) { setError(new ApiError(400, "PRODUCT_REQUIRED", "Choose a product for this sale.", 1)); return; }
    if (!regular && product?.regular_price && discount({ price_minor: toMinor(price, form.currency), currency: form.currency }, product.regular_price) <= 0) {
      setError(new ApiError(400, "NOT_A_DISCOUNT", "A flash price must be below the regular price. To sell at the regular price, choose Regular listing.", 1)); return;
    }
    if (!Number.isFinite(price) || price < 0 || (regular && price <= 0)) { setError(new ApiError(400, "INVALID_PRICE", "Enter a valid price.", 1)); return; }
    if (Date.parse(form.ends_at) <= Date.parse(form.starts_at)) { setError(new ApiError(400, "INVALID_WINDOW", "The sale must end after it starts.", 1)); return; }
    setBusy(true);
    try {
      // A regular listing's price is the product's regular price: keep the catalog in step with it.
      const minor = toMinor(price, form.currency);
      if (regular && product && (product.regular_price?.amount_minor !== minor || product.regular_price?.currency !== form.currency)) {
        const current = await api<ProductView>("/api/admin/products/" + segment(product.product_id), { token });
        await api<ProductView>("/api/admin/products/" + segment(product.product_id), { method: "PUT", token, body: {
          operation_id: operationID(), context: current.context,
          product: { ...current.product, regular_price: { amount_minor: minor, currency: form.currency } } } });
        onProductChanged();
      }
      const body = {
        product_id: form.product_id, price_minor: toMinor(price, form.currency), currency: form.currency, stock: form.stock,
        starts_at: new Date(form.starts_at).toISOString(), ends_at: new Date(form.ends_at).toISOString(),
      };
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
      setPublication(await api<Publication>(`/api/admin/sale-drafts/${segment(draft.sale_id)}/publish`, { method: "POST", token, body: { operation_id: id } }));
    } catch (err) {
      if (!operation && err instanceof ApiError && [400, 401, 403, 404, 409].includes(err.status)) setOperation(undefined);
      setError(err);
    } finally { setBusy(false); }
  }

  if (state?.state === "PUBLISHED" && !busy) {
    return <section className="panel"><h2>Sale published</h2>
      <p>{product?.name} is now {regular ? "listed at its regular price" : "scheduled as a flash sale"}. Inventory holds {form.stock} units for it.</p>
      <div className="catalog-list"><button onClick={() => { onPublished(); onReset(); }}>Schedule another sale</button></div></section>;
  }

  return <section className="panel" aria-label="Schedule a sale">
    <h2>Put a product on sale</h2>
    {products.length === 0 && <p className="empty">Create a product first. Sales can only be scheduled for products in the catalog.</p>}
    {!state && <form onSubmit={save}>
      <fieldset className="form-grid sale-fields" disabled={busy || !!operation || products.length === 0}>
        <div className="full role-picker" role="radiogroup" aria-label="Sale type">
          <button type="button" role="radio" aria-checked={!regular} className={`role ${!regular ? "selected" : ""}`} onClick={() => choose("flash")}>
            <strong>⚡ Flash sale</strong><span>Discounted price, limited stock, short time window.</span></button>
          <button type="button" role="radio" aria-checked={regular} className={`role ${regular ? "selected" : ""}`} onClick={() => choose("regular")}>
            <strong>Regular listing</strong><span>Sell at the product's regular price, for as long as you set.</span></button>
        </div>
        <label className="full">Product<select required value={form.product_id} onChange={e => {
          update("product_id", e.target.value);
          if (regular) choose("regular", products.find(item => item.product_id === e.target.value));
        }}>
          <option value="">Choose a product…</option>
          {products.map(item => <option key={item.product_id} value={item.product_id}>{item.name} ({item.product_id})</option>)}
        </select></label>
        {product && <div className="full chosen"><ProductImage product={product} small /><div><strong>{product.name}</strong>
          {product.regular_price ? <p className="small">Regular price {formatMoney(product.regular_price.amount_minor, product.regular_price.currency)}
            {(() => { const off = Number(form.price) > 0 ? discount({ price_minor: toMinor(Number(form.price), form.currency), currency: form.currency }, product.regular_price) : 0;
              return off > 0 ? <strong className="off"> · this sale is {off}% off</strong> : null; })()}</p>
            : <p className="small">{regular ? "No regular price yet — this listing's price will become it." : "No regular price set, so shoppers will not see a discount."}</p>}
          {regular && <p className="small">Changing the price here also updates the product's regular price.</p>}</div></div>}
        <label>{regular ? "Price (regular)" : "Flash price"}<input inputMode="decimal" required placeholder="499.00" value={form.price}
          onChange={e => update("price", e.target.value)} /></label>
        <label>Currency<select value={form.currency} onChange={e => update("currency", e.target.value)}>
          <option value="INR">INR (₹)</option><option value="USD">USD ($)</option><option value="EUR">EUR (€)</option></select></label>
        <label>{regular ? "Stock available" : "Stock limit"}<input type="number" min="1" max="100000" step="1" required value={Number.isNaN(form.stock) ? "" : form.stock} onChange={e => update("stock", e.target.valueAsNumber)} /></label>
        <span />
        <label>Starts at<input required type="datetime-local" value={form.starts_at} onChange={e => update("starts_at", e.target.value)} /></label>
        <label>Ends at<input required type="datetime-local" value={form.ends_at} onChange={e => update("ends_at", e.target.value)} /></label>
      </fieldset>
      <div className="catalog-list sale-actions"><button disabled={busy || !!operation || products.length === 0}>{draft ? "Save draft changes" : "Save draft"}</button>
        {draft && <button className="secondary" type="button" disabled={busy || (dirty && !operation)} onClick={() => void publish()}>{operation ? "Retry publication" : "Publish sale"}</button>}</div>
      {draft && !dirty && <p role="status">Draft saved. Publishing freezes the terms and prepares Inventory with the stock limit.</p>}
    </form>}
    {state && <div aria-live="polite"><p><span className="pill">{state.state}</span> Publishing through the replication layer and Inventory…</p>
      <Feedback query={progress} /></div>}
    {error !== undefined && <p role="alert" className="error">{message(error)}</p>}
  </section>;
}
