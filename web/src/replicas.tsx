import { useState } from "react";
import { segment } from "./api";
import type { Product, Replica, Version } from "./api";
import { useQuery } from "./hooks";
import { Feedback, ProductImage } from "./ui";

export function Replicas({ token }: { token: string }) {
  const replicas = useQuery<Replica[]>("/api/admin/replicas", token, 3000);
  const products = useQuery<Product[]>("/api/products");
  const [id, setID] = useState("");
  const path = id ? "/api/admin/replicas/products/" + segment(id) : null;
  const versions = useQuery<Version[]>(path, token, 4000);
  const owners = useQuery<string[]>(path ? path + "/owners" : null, token);
  const product = products.data?.find(item => item.product_id === id);
  const healthy = replicas.data?.filter(item => item.backend_ready).length ?? 0;

  return <section className="stack">
    <div><h1>Replicas</h1><p>The Dynamo-style catalog layer: four PostgreSQL-backed replicas, sloppy quorums and hinted handoff.</p></div>
    <div className="panel"><div className="section-head"><h2>Replica health</h2>
      {replicas.data && <span className={`pill ${healthy === replicas.data.length ? "live" : "bad"}`}>{healthy} of {replicas.data.length} ready</span>}</div>
      <Feedback query={replicas} />
      <div className="replicas">{replicas.data?.map(r => <article key={r.replica_id} className={r.backend_ready ? "" : "down"}>
        <h3>Replica {r.replica_id.toUpperCase()}</h3>
        <span className={`pill ${r.backend_ready ? "live" : "bad"}`}>{r.backend_ready ? "Ready" : "Backend unavailable"}</span>
        <p className="small">{r.pending_hints} pending hint{r.pending_hints === 1 ? "" : "s"}{r.pending_hints > 0 && " · held for a peer that is down"}</p>
      </article>)}</div>
    </div>
    <div className="panel"><h2>Inspect a product</h2>
      <p>See which replicas own the product and the version each replica holds.</p>
      <label className="sale-picker">Product<select value={id} onChange={e => setID(e.target.value)}>
        <option value="">Choose a product…</option>
        {products.data?.map(item => <option key={item.product_id} value={item.product_id}>{item.name} ({item.product_id})</option>)}
      </select></label>
      <Feedback query={products} />
      {id && <>
        <div className="chosen"><ProductImage product={product} small /><strong>{product?.name || id}</strong></div>
        <Feedback query={owners} /><Feedback query={versions} />
        {owners.data && <p>Preferred owners <strong>{owners.data.map(owner => owner.toUpperCase()).join(" · ")}</strong></p>}
        {versions.data?.length === 0 && <p>No visible versions.</p>}
        <div className="catalog-list">{versions.data?.map(version => <article key={`${version.replica_id}:${version.counter}`} className="version">
          <strong>{version.product.name}</strong><p className="small">Replica {version.replica_id.toUpperCase()} · version {version.counter}</p>
        </article>)}</div>
        {(versions.data?.length || 0) > 1 && <p className="notice">The replicas hold concurrent versions. Open Products and edit this product to resolve them.</p>}
      </>}
    </div>
  </section>;
}
