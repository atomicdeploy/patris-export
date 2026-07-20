'use strict';

const { createWebView2RendererClient } = require('../src/webview2.cjs');

function createPatrisClient(webview = window.chrome.webview) {
  return createWebView2RendererClient({ webview });
}

module.exports = { createPatrisClient };
