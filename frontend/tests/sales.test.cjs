// Interaction tests execute the shipped script with a small DOM test boundary.
// They exercise API/state transitions; they do not claim visual browser coverage.
const { test } = require('node:test');
const assert = require('node:assert/strict');
const { readFileSync } = require('node:fs');
const { join } = require('node:path');
const vm = require('node:vm');
const source = readFileSync(join(__dirname, '../assets/sales.js'), 'utf8');
class Element {
  constructor() { this.children = []; this.listeners = {}; this.value = ''; this.disabled = false; this.hidden = false; this.text = ''; }
  set textContent(s) { this.text = s; this.children = []; }
  get textContent() { return this.text + this.children.map((e) => e.textContent).join(''); }
  append(node) { this.children.push(node); }
  replaceChildren() { this.text = ''; this.children = []; }
  addEventListener(event, handler) { this.listeners[event] = handler; }
  async fire(event) { return this.listeners[event]({ preventDefault() {}, target: this }); }
  querySelectorAll() { return Object.values(this.fields || {}); }
  reset() { for (const field of Object.values(this.fields)) { field.value = field.defaultValue; field.disabled = false; } }
}
function harness(page, fetcher) {
  const nodes = {}; const node = (id) => nodes[id] ||= new Element();
  const document = { body: { dataset: { page } }, getElementById: node, createElement: () => new Element() };
  const intervals = [];
  let now = Date.parse('2026-10-25T10:00:00Z');
  class ControlledDate extends Date { static now() { return now; } }
  if (page === 'sales') {
    const html = readFileSync(join(__dirname, '../templates/sales.html'), 'utf8');
    const fields = {};
    for (const match of html.matchAll(/<input\s+([^>]+)>/g)) {
      const name = match[1].match(/name="([^"]+)"/)[1]; const field = new Element();
      field.value = field.defaultValue = match[1].match(/value="([^"]*)"/)?.[1] || ''; fields[name] = field;
    }
    node('sale-form').fields = fields; node('sale-form').elements = { namedItem: (name) => fields[name] };
    node('identity-form').elements = { namedItem: () => fields.token };
    node('operator-workspace').hidden = node('readiness-panel').hidden = true;
  }
  const context = { document, Date: ControlledDate, fetch: fetcher, crypto: { randomUUID: () => '11111111-1111-4111-8111-111111111111' }, atob: (s) => Buffer.from(s, 'base64').toString(), FormData: class { constructor(form) { this.form = form; } get(name) { return this.form.elements.namedItem(name).value; } }, setInterval: (fn) => intervals.push(fn), encodeURIComponent, Number, Object, Error, console };
  vm.runInNewContext(source, context);
  return { node, fields: node('sale-form').fields, intervals, advance: (ms) => { now += ms; intervals.forEach((fn) => fn()); } };
}
const token = (role) => `header.${Buffer.from(JSON.stringify({ role })).toString('base64url')}.signature`;
const response = (data, ok = true, boundary = null) => ({ ok, json: async () => data, headers: { get: () => boundary } });
const canonical = { sale_id: '11111111-1111-4111-8111-111111111111', product_id: '22222222-2222-4222-8222-222222222222', version: 1, unit_price_minor: 2500, currency: 'INR', allocated_units: 100, per_shopper_unit_cap: 4, starts_at: '2026-10-25T11:00:00Z', ends_at: '2026-10-25T12:00:00Z', status: 'UPCOMING', admission_limits: { shopper_per_second: 5, sale_per_second: 100, max_in_flight: 50 }, ready_participants: ['admission'] };
test('operator publishes canonical cap, sees overlap error and freezes at original boundary', async () => {
  const calls = []; let reject = false;
  const h = harness('sales', async (path, options) => { calls.push({ path, options }); return reject ? response({ code: 'SALE_WINDOW_CONFLICT' }, false) : response(canonical, true, '2026-10-25T10:30:00Z'); });
  assert.equal(h.fields.per_shopper_unit_cap.value, '1');
  h.fields.token.value = token('SHOPPER'); await h.node('identity-form').fire('submit'); assert.equal(h.node('operator-workspace').hidden, true);
  h.fields.token.value = token('OPERATOR'); await h.node('identity-form').fire('submit'); assert.equal(h.node('operator-workspace').hidden, false); assert.equal(h.fields.token.value, '');
  h.fields.product_id.value = canonical.product_id; h.fields.starts_at.value = '2026-10-25T11:00:00'; h.fields.ends_at.value = '2026-10-25T12:00:00';
  await h.node('sale-form').fire('submit');
  assert.equal(JSON.parse(calls[0].options.body).per_shopper_unit_cap, 1); assert.equal(calls[0].options.method, 'POST');
  assert.equal(h.fields.per_shopper_unit_cap.value, 4); assert.match(h.node('readiness').textContent, /catalog: Awaiting/); assert.match(h.node('readiness').textContent, /admission: Ready/);
  reject = true; await h.node('sale-form').fire('submit'); assert.match(h.node('sale-message').textContent, /overlaps/); assert.equal(calls[1].options.method, 'PUT');
  h.advance(30 * 60000); assert.equal(h.fields.per_shopper_unit_cap.disabled, true); assert.equal(h.fields.starts_at.disabled, true); assert.equal(h.node('save-sale').disabled, true);
  await h.node('new-sale').fire('click'); assert.equal(h.fields.per_shopper_unit_cap.value, '1'); assert.equal(h.fields.per_shopper_unit_cap.disabled, false);
  await h.node('clear-token').fire('click'); assert.equal(h.node('operator-workspace').hidden, true); assert.equal(h.node('readiness-panel').hidden, true);
});
test('storefront keeps metadata conflict visible and fetches authoritative offers', async () => {
  const paths = [];
  const h = harness('products', async (path) => {
    paths.push(path);
    if (path.startsWith('/v1/products?')) return response({ items: [{ product_id: canonical.product_id, metadata_status: 'CONFLICT' }], next_cursor: null });
    if (path.startsWith('/v1/products/')) return response({ product_id: canonical.product_id, value: { name: 'A limited run', description: 'Fixture' }, offers: [{ ...canonical, unit_price_minor: 999 }] });
    return response(canonical);
  });
  await new Promise(setImmediate);
  assert.match(h.node('products').textContent, /conflicting versions/);
  const button = h.node('products').children[0].children.find((node) => node.listeners.click);
  await button.fire('click'); await new Promise(setImmediate);
  assert.ok(paths.includes(`/v1/sales/${canonical.sale_id}/offer`));
  assert.match(h.node('product-detail').textContent, /2500 INR/); assert.doesNotMatch(h.node('product-detail').textContent, /999 INR/); assert.match(h.node('product-detail').textContent, /Up to 4/);
});
