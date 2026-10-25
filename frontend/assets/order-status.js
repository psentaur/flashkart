'use strict';
(() => {
  const $ = (id) => document.getElementById(id);
  let token = '', orderID = '', timer;
  $('order-lookup').elements.namedItem('order_id').value = new URLSearchParams(location.search).get('order_id') || '';
  async function refresh() {
    clearTimeout(timer);
    if (!token || !orderID) return;
    try {
      const response = await fetch(`/v1/orders/${encodeURIComponent(orderID)}`, { headers: { Authorization: `Bearer ${token}` } });
      const order = await response.json();
      if (!response.ok) throw new Error(order.message || 'Order unavailable.');
      $('order-message').textContent = order.state === 'PENDING_PAYMENT' ? 'Payment or reservation settlement is pending. Refreshing…' : `Order ${order.state.toLowerCase()}.`;
      $('order-details').replaceChildren();
      for (const [label, value] of [['Order', order.order_id], ['State', order.state], ['Quantity', order.quantity], ['Total (minor units)', `${order.total_minor} ${order.currency}`], ['Reservation expiry', order.expires_at], ['Refund', order.refund_status]]) {
        for (const [tag, text] of [['dt', label], ['dd', value]]) { const node = document.createElement(tag); node.textContent = String(text); $('order-details').append(node); }
      }
      if (order.state === 'PENDING_PAYMENT') timer = setTimeout(refresh, 1000);
    } catch (error) { $('order-message').textContent = `${error.message} The last known outcome has not changed. Submit again to refresh.`; }
  }
  $('order-identity').addEventListener('submit', (event) => { event.preventDefault(); const input = event.target.elements.namedItem('token'); token = input.value.trim(); input.value = ''; refresh(); });
  $('clear-order-token').addEventListener('click', () => { token = ''; clearTimeout(timer); $('order-details').replaceChildren(); $('order-message').textContent = 'Signed out.'; });
  $('order-lookup').addEventListener('submit', (event) => { event.preventDefault(); orderID = event.target.elements.namedItem('order_id').value; refresh(); });
})();
