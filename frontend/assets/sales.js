const $ = (id) => document.getElementById(id);
const message = (id, text) => { $(id).textContent = text; };
const element = (tag, text, parent) => { const node = document.createElement(tag); node.textContent = text; parent.append(node); return node; };
const errors = {
  SALE_WINDOW_CONFLICT: 'Another sale for this product overlaps this window. Choose an adjacent or separate window.',
  VERSION_CONFLICT: 'This version changed or the sale is frozen. Load the current sale before trying again.',
  METADATA_CONFLICT: 'Product metadata has conflicting versions. An operator must resolve them.',
  QUORUM_UNAVAILABLE: 'Product metadata is temporarily unavailable. Try again shortly.',
  UNAUTHENTICATED: 'Your token is missing, invalid or expired.',
  FORBIDDEN: 'An operator identity is required.',
};
async function request(path, options = {}) {
  const response = await fetch(path, { ...options, cache: 'no-store' });
  let data; try { data = await response.json(); } catch { throw new Error('The service returned an invalid response.'); }
  if (!response.ok) throw new Error(errors[data.code] || data.message || 'The service is temporarily unavailable.');
  return { data, freezeAt: response.headers.get('X-Sale-Freeze-At') };
}
function offerView(offer, target) {
  const card = element('article', '', target); card.className = 'offer';
  element('span', offer.status, card).className = 'badge';
  element('h3', `${offer.unit_price_minor} ${offer.currency} minor units`, card);
  element('p', `Up to ${offer.per_shopper_unit_cap} unit(s) per shopper, across held and confirmed purchases.`, card);
  element('p', `${offer.starts_at} → ${offer.ends_at}`, card);
  element('small', `Sale ${offer.sale_id} · version ${offer.version}`, card);
}
async function showOffer(id, target) {
  try { const { data } = await request(`/v1/sales/${encodeURIComponent(id)}/offer`); offerView(data, target); }
  catch (error) { element('p', error.message, target); }
}
if (document.body.dataset.page === 'products') {
  let cursor = null;
  async function products(reset = true) {
    if (reset) { $('products').replaceChildren(); cursor = null; }
    message('catalog-message', 'Loading products…');
    try {
      const { data } = await request(`/v1/products?limit=20${cursor ? `&cursor=${encodeURIComponent(cursor)}` : ''}`);
      for (const product of data.items) {
        const card = element('article', '', $('products')); card.className = 'product-card';
        element('span', product.metadata_status, card).className = 'badge';
        element('h3', product.value?.name || 'Product details unavailable', card);
        element('p', product.value?.description || (product.metadata_status === 'CONFLICT' ? errors.METADATA_CONFLICT : errors.QUORUM_UNAVAILABLE), card);
        const button = element('button', 'View release', card); button.className = 'secondary';
        button.addEventListener('click', () => productDetail(product.product_id));
      }
      cursor = data.next_cursor; $('more-products').hidden = !cursor;
      message('catalog-message', data.items.length ? '' : 'No products are listed yet.');
    } catch (error) { message('catalog-message', error.message); }
  }
  async function productDetail(id) {
    const target = $('product-detail'); target.replaceChildren();
    try {
      const { data } = await request(`/v1/products/${encodeURIComponent(id)}`);
      element('h3', data.value.name, target); element('p', data.value.description, target);
      for (const offer of data.offers) await showOffer(offer.sale_id, target);
      if (!data.offers.length) element('p', 'No sale offers are listed for this product.', target);
    } catch (error) { element('p', error.message, target); }
  }
  $('refresh-products').addEventListener('click', () => products());
  $('more-products').addEventListener('click', () => products(false));
  $('product-lookup').addEventListener('submit', (event) => { event.preventDefault(); productDetail(new FormData(event.target).get('product_id')); });
  $('offer-lookup').addEventListener('submit', (event) => { event.preventDefault(); $('offer-detail').replaceChildren(); showOffer(new FormData(event.target).get('sale_id'), $('offer-detail')); });
  products();
}
if (document.body.dataset.page === 'sales') {
  let token = ''; let current = null; let freezeAt = null; let wasFrozen = false;
  const form = $('sale-form');
  const field = (name) => form.elements.namedItem(name);
  const auth = () => ({ Authorization: `Bearer ${token}`, 'Content-Type': 'application/json' });
  const localTime = (utc) => { const d = new Date(utc); return new Date(d.getTime() - d.getTimezoneOffset() * 60000).toISOString().slice(0, 19); };
  function freeze() {
    const frozen = current && (current.status === 'CANCELLED' || Date.now() >= Date.parse(freezeAt || current.starts_at));
    for (const input of form.querySelectorAll('input')) input.disabled = !!frozen;
    if (current) { field('sale_id').disabled = true; field('product_id').disabled = true; }
    $('save-sale').disabled = !!frozen; $('cancel-sale').disabled = !current || !!frozen;
    if (frozen && !wasFrozen && current.status !== 'CANCELLED') message('sale-message', 'This sale is frozen. Price, allocation, cap and schedule cannot be changed.');
    wasFrozen = !!frozen;
  }
  function display(sale, boundary) {
    current = sale; freezeAt = boundary || freezeAt || sale.starts_at;
    for (const key of ['sale_id', 'product_id', 'unit_price_minor', 'currency', 'allocated_units', 'per_shopper_unit_cap']) field(key).value = sale[key];
    field('starts_at').value = localTime(sale.starts_at); field('ends_at').value = localTime(sale.ends_at);
    for (const [key, value] of Object.entries(sale.admission_limits)) field(key).value = value;
    $('save-sale').textContent = 'Replace configuration';
    $('readiness-panel').hidden = false; $('sale-summary').replaceChildren(); offerView(sale, $('sale-summary'));
    $('readiness').replaceChildren();
    for (const participant of ['catalog', 'admission', 'inventory']) element('li', `${participant}: ${sale.ready_participants.includes(participant) ? 'Ready for this version' : 'Awaiting matching confirmation'}`, $('readiness'));
    freeze();
  }
  async function load(id) {
    try { const result = await request(`/v1/admin/sales/${encodeURIComponent(id)}`, { headers: auth() }); freezeAt = null; wasFrozen = false; display(result.data, result.freezeAt); message('sale-message', 'Current configuration loaded.'); freeze(); }
    catch (error) { message('sale-message', error.message); }
  }
  $('identity-form').addEventListener('submit', (event) => {
    event.preventDefault(); const input = event.target.elements.namedItem('token'); const candidate = input.value.trim(); input.value = '';
    // This gate is presentation only. Every operator API validates the signed JWT.
    try { const claims = JSON.parse(atob(candidate.split('.')[1].replace(/-/g, '+').replace(/_/g, '/'))); if (claims.role !== 'OPERATOR') throw new Error(); }
    catch { token = ''; $('operator-workspace').hidden = true; message('identity-message', 'An operator token is required.'); return; }
    token = candidate; $('operator-workspace').hidden = false; message('identity-message', 'Operator token loaded for this page.');
  });
  $('clear-token').addEventListener('click', () => { token = ''; current = null; wasFrozen = false; $('operator-workspace').hidden = true; $('readiness-panel').hidden = true; message('identity-message', 'Signed out.'); });
  $('load-sale').addEventListener('submit', (event) => { event.preventDefault(); load(new FormData(event.target).get('sale_id')); });
  $('refresh-sale').addEventListener('click', () => { if (current) load(current.sale_id); });
  $('new-sale').addEventListener('click', () => { current = null; freezeAt = null; wasFrozen = false; form.reset(); for (const input of form.querySelectorAll('input')) input.disabled = false; field('sale_id').value = crypto.randomUUID(); $('readiness-panel').hidden = true; $('save-sale').textContent = 'Publish sale'; $('save-sale').disabled = false; $('cancel-sale').disabled = true; message('sale-message', ''); });
  form.addEventListener('submit', async (event) => {
    event.preventDefault(); const body = { expected_version: current?.version || 0, admission_limits: {} };
    for (const key of ['sale_id', 'product_id', 'currency']) body[key] = field(key).value;
    for (const key of ['unit_price_minor', 'allocated_units', 'per_shopper_unit_cap']) body[key] = Number(field(key).value);
    for (const key of ['shopper_per_second', 'sale_per_second', 'max_in_flight']) body.admission_limits[key] = Number(field(key).value);
    if (![body.unit_price_minor, body.allocated_units, body.per_shopper_unit_cap, ...Object.values(body.admission_limits)].every((n) => Number.isSafeInteger(n) && n > 0)) { message('sale-message', 'Enter positive whole numbers within the supported bounds.'); return; }
    body.starts_at = new Date(field('starts_at').value).toISOString(); body.ends_at = new Date(field('ends_at').value).toISOString();
    if (body.starts_at >= body.ends_at) { message('sale-message', 'Start must be before end.'); return; }
    try {
      const path = current ? `/v1/admin/sales/${encodeURIComponent(current.sale_id)}` : '/v1/admin/sales';
      const result = await request(path, { method: current ? 'PUT' : 'POST', headers: auth(), body: JSON.stringify(body) });
      display(result.data, result.freezeAt); message('sale-message', 'Configuration saved. Waiting for matching readiness.'); freeze();
    } catch (error) { message('sale-message', error.message); }
  });
  $('cancel-sale').addEventListener('click', async () => {
    if (!current) return;
    try { const result = await request(`/v1/admin/sales/${encodeURIComponent(current.sale_id)}/cancel`, { method: 'POST', headers: auth(), body: JSON.stringify({ expected_version: current.version }) }); display(result.data, result.freezeAt); message('sale-message', 'Sale cancelled. Its schedule window is available again.'); }
    catch (error) { message('sale-message', error.message); }
  });
  field('sale_id').value = crypto.randomUUID();
  setInterval(freeze, 1000);
}
