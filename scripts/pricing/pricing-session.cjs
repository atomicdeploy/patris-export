'use strict';

// Reuse the existing authenticated WordPress user session and dispatcher.
async function openPricingSession({ siteUrl, authorization, timeoutMs = 15000 }) {
  const site = new URL(siteUrl);
  if (site.protocol !== 'https:' || site.username || site.password || site.pathname !== '/' || site.search || site.hash) throw Error('invalid_session_origin');
  if (typeof WebSocket !== 'function') throw Error('node_websocket_required');
  const response = await fetch(site.origin + '/wp-json/digitalogic/v1/websocket/config', {
    headers: { Authorization: authorization }, redirect: 'error', signal: AbortSignal.timeout(timeoutMs),
  });
  if (!response.ok) { await response.body?.cancel(); throw Error('session_authentication_failed'); }
  const config = await response.json();
  const endpoint = new URL(config.url);
  if (!config.enabled || endpoint.protocol !== 'wss:' || endpoint.host !== site.host
      || endpoint.username || endpoint.password || endpoint.search || endpoint.hash
      || !/^[A-Za-z0-9]{48}$/.test(config.token || '')) throw Error('session_configuration_invalid');
  endpoint.searchParams.set('token', config.token);
  const socket = new WebSocket(endpoint);
  let sequence = 0, pending = null;
  await new Promise((resolve, reject) => {
    const timer = setTimeout(() => { socket.close(); reject(Error('session_connect_timeout')); }, timeoutMs);
    socket.onopen = () => { clearTimeout(timer); resolve(); };
    socket.onerror = () => { clearTimeout(timer); reject(Error('session_connect_failed')); };
  });
  const fail = () => { if (pending) { clearTimeout(pending.timer); pending.reject(Error('session_connection_lost')); pending = null; } };
  socket.onerror = fail;
  socket.onclose = fail;
  socket.onmessage = event => {
    if (typeof event.data !== 'string' || event.data.length > 65536) { fail(); socket.close(); return; }
    let message;
    try { message = JSON.parse(event.data); } catch { return; }
    if (!pending || message.id !== pending.id) return;
    const current = pending; pending = null; clearTimeout(current.timer);
    current.resolve(message);
  };
  return {
    close() { socket.close(); },
    request(productCode) {
      if (pending || socket.readyState !== WebSocket.OPEN) return Promise.reject(Error('session_not_ready'));
      return new Promise((resolve, reject) => {
        const id = 'pricing-' + (++sequence);
        const timer = setTimeout(() => { pending = null; socket.close(); reject(Error('request_timeout')); }, timeoutMs);
        pending = { id, timer, resolve, reject };
        socket.send(JSON.stringify({ id, command: 'digitalogic_recalculate_product_price', data: { product_code: productCode } }));
      });
    },
  };
}
module.exports = { openPricingSession };
