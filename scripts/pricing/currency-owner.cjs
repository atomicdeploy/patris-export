'use strict';

// Transport only: WordPress owns currency dates and the selected pricing engine.
async function runCurrency({ requestJSON, requestId, values = {}, observeOnly = false,
  siteUrl = 'https://digitalogic.ir', timeoutMs = 180000,
  key = process.env.DIGITALOGIC_PRICING_WRITE_KEY,
  secret = process.env.DIGITALOGIC_PRICING_WRITE_SECRET }) {
  if (!/^[a-zA-Z0-9._:-]{8,128}$/.test(requestId || '')) throw Error('invalid_request_id');
  const site = new URL(siteUrl);
  if (site.protocol !== 'https:' || site.username || site.password || site.pathname !== '/' || site.search || site.hash) throw Error('invalid_site_url');
  if (!/^ck_[a-f0-9]{40}$/.test(key || '') || !/^cs_[a-f0-9]{40}$/.test(secret || '')) throw Error('separate_woocommerce_write_credentials_required');
  if (!Number.isSafeInteger(timeoutMs) || timeoutMs < 1 || timeoutMs > 600000) throw Error('invalid_timeout_ms');
  const allowed = ['yuan_price', 'dollar_price', 'cny_effective_date', 'usd_effective_date'];
  for (const [field, value] of Object.entries(values)) {
    if (!allowed.includes(field) || typeof value !== 'string') throw Error('invalid_currency_fields');
    if (field.endsWith('_price') ? !/^[1-9][0-9]{0,9}$/.test(value)
      : !/^\d{4}-\d{2}-\d{2}$/.test(value) || !Number.isFinite(Date.parse(value)) || new Date(value).toISOString().slice(0, 10) !== value) throw Error('invalid_currency_value');
  }
  if (!observeOnly && !Object.keys(values).length) throw Error('currency_fields_required');
  if (observeOnly && Object.keys(values).length) throw Error('status_cannot_change_currency');
  const started = performance.now();
  const authorization = 'Basic ' + Buffer.from(key + ':' + secret).toString('base64');
  const remaining = () => { const n = timeoutMs - Math.round(performance.now() - started); if (n < 1) throw Error('observation_timeout'); return n; };
  const call = (path, extra = {}) => requestJSON(site.origin, '/wp-json/digitalogic/v1/' + path, { authorization, timeoutMs: remaining(), ...extra });
  const result = { request_id: requestId, operation: observeOnly ? 'currency_status' : 'currency_update', delivered: false, outcome: 'not_started', exit_code: 1 };
  try {
    let response;
    if (!observeOnly) {
      const current = await call('currency');
      const revision = current?.data?.state_revision;
      if (current?.success !== true || !/^sha256:[a-f0-9]{64}$/.test(revision || '')) throw Error('owner_state_unverified');
      result.outcome = 'unknown_delivery_outcome';
      response = await call('currency', { body: { ...values, request_id: requestId, expected_state_revision: revision },
        identityHeaders: { 'If-Match': '"' + revision + '"', 'Idempotency-Key': requestId } });
    } else {
      result.outcome = 'unknown_delivery_outcome';
      response = await call('currency/requests/' + encodeURIComponent(requestId));
    }
    while (true) {
      const job = response?.data;
      if (response?.success !== true || !job || !/^[a-f0-9]{32}$/.test(job.job_id || '') || !Number.isSafeInteger(job.generation) || job.generation < 1 || job.request_id !== requestId) throw Error('job_identity_unverified');
      if (result.job_id && (result.job_id !== job.job_id || result.generation !== job.generation)) throw Error('job_identity_changed');
      result.job_id = job.job_id; result.generation = job.generation; result.status = job.status;
      if (job.status === 'confirmed') {
        const readback = await call('currency');
        if (readback?.success !== true || !/^sha256:[a-f0-9]{64}$/.test(readback.data?.state_revision || '')) throw Error('owner_readback_unverified');
        const expected = job.desired_currency;
        if (!observeOnly && Object.entries(values).some(([k,v]) => String(expected?.[k]) !== v)) throw Error('admitted_values_differ');
        if (!expected || !Object.keys(expected).length || Object.entries(expected).some(([k,v]) => String(readback.data[k]) !== String(v))) throw Error('owner_readback_changed');
        result.delivered = true; result.outcome = 'confirmed'; result.exit_code = 0;
        result.currency = Object.fromEntries(['yuan_price','dollar_price','cny_effective_date','usd_effective_date'].map(k=>[k,readback.data[k]]));
        result.readiness_after = true;
        break;
      }
      if (!['queued','running','publishing','awaiting_delivery','cancelling'].includes(job.status)) {
        result.outcome = 'terminal_not_confirmed'; break;
      }
      await new Promise(resolve => setTimeout(resolve, Math.min(1000, remaining())));
      response = await call('currency/requests/' + encodeURIComponent(requestId));
    }
  } catch (error) {
    result.error = /^[a-z_0-9]+$/.test(error.message) ? error.message : 'currency_request_failed';
  }
  result.elapsed_ms = Math.round(performance.now() - started);
  return result;
}
module.exports = { runCurrency };
