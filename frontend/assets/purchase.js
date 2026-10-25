'use strict';
(() => {
  const $ = (id) => document.getElementById(id);
  let token = '', offer = null, purchase = null, key = '', busy = false;
  const message = (text) => { $('purchase-message').textContent = text; };
  function clearPurchase() { purchase = null; key = ''; $('purchase-key').textContent = ''; $('retry-purchase').hidden = true; $('new-purchase').hidden = true; $('order-link').hidden = true; }
  function terms() {
    $('offer-terms').replaceChildren();
    for (const [label, value] of [['Price (minor units)', `${offer.unit_price_minor} ${offer.currency}`], ['Offer version', offer.version], ['Per-shopper unit cap', offer.per_shopper_unit_cap], ['Status', offer.status], ['Ends', offer.ends_at]]) {
      for (const [tag, text] of [['dt', label], ['dd', value]]) { const node = document.createElement(tag); node.textContent = String(text); $('offer-terms').append(node); }
    }
    $('checkout-terms').hidden = false;
    $('purchase-submit').disabled = offer.status !== 'ACTIVE';
  }
  $('shopper-identity').addEventListener('submit', (event) => { event.preventDefault(); const input = event.target.elements.namedItem('token'); token = input.value.trim(); input.value = ''; $('identity-message').textContent = 'Shopper token loaded for this page.'; });
  $('clear-shopper').addEventListener('click', () => { token = ''; $('identity-message').textContent = 'Signed out.'; });
  $('checkout-offer').addEventListener('submit', async (event) => {
    event.preventDefault();
    // Do not replace an uncertain purchase with newly loaded terms.
    if (purchase) { message('Retry the saved purchase or explicitly start a new purchase first.'); return; }
    try { const id = event.target.elements.namedItem('sale_id').value; const response = await fetch(`/v1/sales/${encodeURIComponent(id)}/offer`); const data = await response.json(); if (!response.ok) throw new Error(data.message); offer = data; terms(); message('Review the price, currency and cap, then confirm.'); }
    catch (error) { message(error.message || 'Offer unavailable.'); }
  });
  async function submit() {
    if (busy || !purchase) return;
    if (!token) { message('Load your shopper token first.'); return; }
    busy = true; $('purchase-submit').disabled = true; $('retry-purchase').disabled = true;
    try {
      const response = await fetch('/v1/purchases', { method: 'POST', headers: { Authorization: `Bearer ${token}`, 'Content-Type': 'application/json', 'Idempotency-Key': key }, body: JSON.stringify(purchase) });
      const result = await response.json();
      $('retry-purchase').hidden = true; $('new-purchase').hidden = true;
      if (response.ok) {
        message(result.state === 'CONFIRMED' ? 'Purchase confirmed.' : 'Reservation accepted. Awaiting payment confirmation.');
        $('order-link').href = `/orders?order_id=${encodeURIComponent(result.order_id)}`; $('order-link').hidden = false;
        $('new-purchase').hidden = false;
      } else if (result.code === 'OFFER_CHANGED' && result.current_offer) {
        offer = result.current_offer; clearPurchase(); terms(); message('The offer changed. Review these new terms and confirm before submitting a new purchase.');
      } else if (result.code === 'PURCHASE_LIMIT_EXCEEDED') {
        message('Your purchase limit for this sale has been reached. This request key keeps its rejection. After a hold is released or expires, explicitly start a new purchase. Confirmed units still count.');
        $('new-purchase').hidden = false;
      } else {
        message(result.message || result.code || 'Purchase unavailable.');
        $('retry-purchase').hidden = !result.retryable;
        $('new-purchase').hidden = !!result.retryable;
      }
    } catch { message('The outcome is unknown. Retry the same purchase with its saved request key.'); $('retry-purchase').hidden = false; }
    finally { busy = false; $('retry-purchase').disabled = false; $('purchase-submit').disabled = !!purchase || offer?.status !== 'ACTIVE'; }
  }
  $('purchase-form').addEventListener('submit', (event) => {
    event.preventDefault(); if (!offer || purchase || busy) return;
    if (!token) { message('Load your shopper token first.'); return; }
    const quantity = Number(event.target.elements.namedItem('quantity').value);
    if (!Number.isInteger(quantity) || quantity < 1 || quantity > 100 || !Number.isSafeInteger(quantity * offer.unit_price_minor)) { message('Enter a quantity from 1 to 100 within the supported total.'); return; }
    purchase = { sale_id: offer.sale_id, product_id: offer.product_id, quantity, expected_offer_version: offer.version, expected_unit_price_minor: offer.unit_price_minor, currency: offer.currency };
    key = crypto.randomUUID(); $('purchase-key').textContent = `Request key: ${key}. Keep this page open while retrying.`; return submit();
  });
  $('retry-purchase').addEventListener('click', submit);
  $('new-purchase').addEventListener('click', () => { if (busy) return; clearPurchase(); offer = null; $('checkout-terms').hidden = true; message('Review the current offer for your new purchase.'); });
})();
