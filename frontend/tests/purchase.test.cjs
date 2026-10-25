const { test } = require('node:test');
const assert = require('node:assert/strict');
const { readFileSync } = require('node:fs');
const vm = require('node:vm');
const source = readFileSync(`${__dirname}/../assets/purchase.js`, 'utf8');
class Node {
  constructor() { this.value = ''; this.hidden = false; this.disabled = false; this.textContent = ''; this.listeners = {}; this.children = []; this.elements = { namedItem: (name) => this.fields[name] }; this.fields = {}; }
  addEventListener(name, fn) { this.listeners[name] = fn; }
  async fire(name) { return this.listeners[name]({ preventDefault() {}, target: this }); }
  append(node) { this.children.push(node); }
  replaceChildren() { this.children = []; }
}
function setup(outcomes) {
  const nodes = {}; const $ = (id) => nodes[id] ||= new Node(); let sequence = 0; const calls = [];
  for (const [id, field] of [['shopper-identity', 'token'], ['checkout-offer', 'sale_id'], ['purchase-form', 'quantity']]) $(id).fields[field] = new Node();
  $('purchase-form').fields.quantity.value = '1';
  const offer = { sale_id: 'sale', product_id: 'product', version: 1, unit_price_minor: 2500, currency: 'INR', per_shopper_unit_cap: 1, status: 'ACTIVE', ends_at: '2026-10-26T12:00:00Z' };
  vm.runInNewContext(source, { document: { getElementById: $, createElement: () => new Node() }, crypto: { randomUUID: () => `key-${++sequence}` }, fetch: async (url, options) => {
    if (url.includes('/offer')) return { ok: true, json: async () => offer };
    calls.push(JSON.parse(JSON.stringify(options))); const outcome = outcomes.shift(); if (outcome instanceof Error) throw outcome;
    return { ok: outcome.status < 400, json: async () => outcome.body };
  } });
  async function ready() { $('shopper-identity').fields.token.value = 'private-token'; await $('shopper-identity').fire('submit'); await $('checkout-offer').fire('submit'); }
  return { $, calls, ready, offer };
}
test('transport timeout and retry keep the original key and payload; token stays in memory', async () => {
  const h = setup([new Error('timeout'), { status: 202, body: { order_id: 'order', state: 'PENDING_PAYMENT' } }]);
  await h.ready(); await h.$('purchase-form').fire('submit');
  assert.equal(h.$('retry-purchase').hidden, false);
  h.$('purchase-form').fields.quantity.value = '2'; await h.$('retry-purchase').fire('click');
  assert.equal(h.calls[0].headers['Idempotency-Key'], h.calls[1].headers['Idempotency-Key']);
  assert.equal(h.calls[0].body, h.calls[1].body); assert.equal(h.$('shopper-identity').fields.token.value, '');
  assert.equal(h.$('order-link').href, '/orders?order_id=order');
});
test('cap rejection never changes its key automatically; later purchase requires explicit action', async () => {
  const h = setup([{ status: 409, body: { code: 'PURCHASE_LIMIT_EXCEEDED', retryable: false } }, { status: 202, body: { order_id: 'later' } }]);
  await h.ready(); await h.$('purchase-form').fire('submit');
  assert.match(h.$('purchase-message').textContent, /purchase limit/); assert.equal(h.$('retry-purchase').hidden, true);
  await h.$('purchase-form').fire('submit'); assert.equal(h.calls.length, 1);
  await h.$('new-purchase').fire('click'); await h.$('checkout-offer').fire('submit'); await h.$('purchase-form').fire('submit');
  assert.notEqual(h.calls[0].headers['Idempotency-Key'], h.calls[1].headers['Idempotency-Key']);
});
test('changed offer needs explicit confirmation before a new request', async () => {
  const current = { sale_id: 'sale', product_id: 'product', version: 2, unit_price_minor: 3000, currency: 'INR', per_shopper_unit_cap: 2, status: 'ACTIVE' };
  const h = setup([{ status: 409, body: { code: 'OFFER_CHANGED', current_offer: current } }, { status: 202, body: { order_id: 'order' } }]);
  await h.ready(); await h.$('purchase-form').fire('submit'); assert.equal(h.calls.length, 1);
  await h.$('purchase-form').fire('submit'); assert.equal(JSON.parse(h.calls[1].body).expected_unit_price_minor, 3000);
  assert.notEqual(h.calls[0].headers['Idempotency-Key'], h.calls[1].headers['Idempotency-Key']);
});
