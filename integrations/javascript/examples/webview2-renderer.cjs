'use strict';

const { createWebView2RendererClient } = require('..');

function createPatrisClient(webview = window.chrome.webview) {
  return createWebView2RendererClient({ webview });
}

module.exports = { createPatrisClient };
