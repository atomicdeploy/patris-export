# Patris Export JavaScript host adapters

These CommonJS adapters define one Patris Export renderer contract for Electron,
Tauri, and WebView2 without moving database, transformation, pricing, or export
logic into JavaScript. Electron has a directly usable privileged-host adapter;
Tauri and WebView2 have renderer adapters plus framework-neutral host-routing
references that native applications must bind in their privileged layer.
`cmd/patris-export-lib` remains the only native ABI and the normal Patris
executable and REST API remain the fallbacks.

See the adapter-specific [`CHANGELOG.md`](CHANGELOG.md) for release notes.
All supported JavaScript and TypeScript APIs are exported from the package
root. The `src/` file layout is internal and is not a public subpath contract.

The security boundary is deliberate:

```text
untrusted renderer
  -> typed allowlisted IPC/message call
  -> privileged host (validates source URL plus host-owned session/capability)
  -> Patris DLL worker
  -> Patris executable on loopback
  -> configured Patris REST endpoint
```

No adapter creates or transfers an authentication token. The privileged host
must resolve application login and capabilities from its own trusted session
state on every call. The Patris bridge is an integration transport, not a
second login system.

For Digitalogic, keep the existing WordPress session and capability checks on
the canonical `/panel/` route. Do not exchange that browser session for a bearer
token or forward a renderer-supplied credential through this bridge. Electron
registration requires a privileged `authorize` callback for every status and
method call. Origin comparison alone is not authorization: all same-origin
WordPress routes share one origin even when the current session or route lacks
panel capabilities.

## Stable renderer contract

Every renderer receives only `status()` and `call(method, params)`. The methods
match ABI version 1 exactly:

- `app.get`
- `records.list`
- `info.get`
- `status.get`
- `config.get`
- `config.set`
- `toast.show`
- `refresh`

Success:

```json
{
  "ok": true,
  "result": {
    "1001": { "name": "Example module", "final_price": "150000" }
  },
  "meta": { "requestId": 1, "method": "records.list", "mode": "dll" }
}
```

Failure:

```json
{
  "ok": false,
  "error": {
    "code": "RUNTIME_UNAVAILABLE",
    "message": "No configured Patris runtime could be started.",
    "retryable": true
  },
  "meta": { "requestId": 1, "method": "records.list" }
}
```

The renderer never chooses a DLL path, executable path, REST base URL, origin,
or method outside this allowlist. Startup may fall back to the next configured
runtime. A failed data-changing call is never automatically replayed through a
different transport.

## Shared privileged host

`createConfiguredHost` applies DLL -> executable -> REST startup order:

```js
const path = require('node:path');
const { createConfiguredHost } = require('@atomicdeploy/patris-export-hosts');

const host = createConfiguredHost({
  allowedOrigins: ['app://digitalogic', 'https://digitalogic.ir'],
  dll: {
    // The existing Digitalogic Node-API bridge. The DLL is loaded only in the
    // child worker created by the privileged host.
    addonPath: path.join(process.resourcesPath, 'app.asar.unpacked', 'native', 'digitalogic_patris.node'),
    dllPath: path.join(process.resourcesPath, 'patris', 'patris-export.dll'),
    engineOptions: {
      database_path: 'C:\\Patris\\data4\\kala.db',
      watch: true,
      watch_set: true
    }
  },
  executable: {
    executablePath: path.join(process.resourcesPath, 'patris', 'patris-export.exe'),
    databasePath: 'C:\\Patris\\data4\\kala.db',
    watch: true
  },
  rest: {
    baseUrl: 'https://patris.example.test'
  }
});
```

The DLL worker accepts the existing Digitalogic Node-API addon interface and
verifies ABI version 1 plus the canonical capabilities before creating an
engine. The worker serializes calls, owns the engine handle, frees native
strings through the addon, and closes before it exits.

Digitalogic Electron already has a `PatrisBridge` with utility-process and
fallback management. Keep that implementation and wrap the instance rather
than implementing a second bridge:

```js
const {
  PrivilegedPatrisHost,
  wrapExistingElectronBridge
} = require('@atomicdeploy/patris-export-hosts');

const host = new PrivilegedPatrisHost({
  allowedOrigins: ['app://digitalogic', 'https://digitalogic.ir'],
  transports: [wrapExistingElectronBridge(new PatrisBridge(settings.patris))]
});
```

See [`examples/electron-main.cjs`](examples/electron-main.cjs).

## Electron

Register handlers in the main process and expose the renderer client from a
sandboxed preload:

```js
const { app, ipcMain } = require('electron');
const {
  registerElectronPatrisHost,
  registerElectronShutdownBarrier
} = require('@atomicdeploy/patris-export-hosts');

const unregister = registerElectronPatrisHost({
  ipcMain,
  host,
  authorize: async (event, { sourceUrl, method }) => {
    const url = new URL(sourceUrl);
    if (!url.pathname.startsWith('/panel/')) return false;
    // This callback runs in the main process. Resolve the existing WordPress
    // session/capability state from event.sender/session; never inspect a
    // renderer-supplied token or authorization flag.
    return wordpressSession.can(event.sender, method === 'config.set'
      ? 'manage_woocommerce'
      : 'read_digitalogic_panel');
  }
});
registerElectronShutdownBarrier({
  app,
  cleanup: async () => {
    unregister();
    await host.close();
  },
  onError: (error) => console.error('Patris shutdown failed', error)
});
```

Use the self-contained [`examples/electron-preload.cjs`](examples/electron-preload.cjs)
with `contextIsolation: true`, `sandbox: true`, and `nodeIntegration: false`.
It requires only Electron's sandbox-provided module; do not add local-file or
package imports to that preload unless the host application's build first
bundles them into the same file.

Electron origin checks use `event.senderFrame.url`; a URL sent inside the
renderer payload is ignored. Prefer a registered `app://digitalogic` scheme for
packaged local content instead of allowing the broad `file://` origin. The
authorizer receives the privileged Electron event plus derived source/action/
method metadata and must return exactly `true`; missing, throwing, or false
authorizers fail closed. The shutdown barrier prevents the first `before-quit`,
awaits cleanup once, removes itself, and only then re-enters `app.quit()` so
Electron cannot exit ahead of the DLL worker or sidecar.

## Tauri

Bundle the package-root renderer client into the web frontend and supply
Tauri's `invoke` function:

```js
const { invoke } = require('@tauri-apps/api/core');
const {
  createTauriRendererClient
} = require('@atomicdeploy/patris-export-hosts');
const patris = createTauriRendererClient({ invoke });
const response = await patris.call('records.list');
```

Implement two privileged commands, `patris_invoke` and `patris_status`. They
must derive the caller URL/window label from Tauri's command context, enforce
an exact origin allowlist, and authorize the host-owned session/capability on
every call. The JavaScript reference handler makes that fail-closed contract
explicit:

```js
const {
  createTauriCommandHandlers
} = require('@atomicdeploy/patris-export-hosts');

const commands = createTauriCommandHandlers({
  host,
  getSourceUrl: (event) => event.privilegedWindowUrl,
  authorize: async (event, { sourceUrl, method }) => {
    const url = new URL(sourceUrl);
    return url.pathname.startsWith('/panel/')
      && wordpressSession.can(event.windowLabel, method === 'config.set'
        ? 'manage_woocommerce'
        : 'read_digitalogic_panel');
  }
});
```

Missing, throwing, or false authorizers deny the request. Never accept an
origin, path, native handle, endpoint, auth flag, or credential from the
command payload. In a native Tauri build, load
`patris-export.dll`/`libpatris-export.so` from the signed application resource
directory or launch the Patris sidecar; JavaScript must not call `libloading`,
FFI plugins, or shell commands directly.

The JavaScript renderer adapter and framework-neutral command-routing reference
are covered by the Node test suite. This repository does not ship a compiled
Tauri/Rust command plugin, and this workstation currently has `rustup` but no
installed Rust toolchain, so no native Tauri build is claimed. A production
Tauri application must implement and test the two commands in its privileged
Rust core (or a signed sidecar) before calling that integration native-ready.
This does not affect Electron, the C ABI, or the Patris executable fallback.

## WebView2

Bundle the package-root renderer client into trusted app content:

```js
const {
  createWebView2RendererClient
} = require('@atomicdeploy/patris-export-hosts');
const patris = createWebView2RendererClient({ webview: window.chrome.webview });
const response = await patris.call('records.list');
```

The JavaScript host-routing reference requires the same independent
authorization decision:

```js
const {
  createWebView2MessageHandler
} = require('@atomicdeploy/patris-export-hosts');

const handleMessage = createWebView2MessageHandler({
  host,
  getSourceUrl: (event) => event.privilegedSourceUrl,
  authorize: (event, { sourceUrl, method }) => {
    const url = new URL(sourceUrl);
    return url.pathname.startsWith('/panel/')
      && wordpressSession.can(event.webviewId, method === 'config.set'
        ? 'manage_woocommerce'
        : 'read_digitalogic_panel');
  },
  postMessage: (payload, event) => event.reply(payload)
});
```

The native .NET/C++ host listens for `patris:request`, reads the source from the
WebView2 event/navigation state, checks an exact origin such as
`https://patris.local`, resolves the host-owned login/capability state, and
answers with `patris:result`. `createWebView2MessageHandler` is the JavaScript
reference harness; its required `authorize(event, context)` callback runs for
every valid request and denies on missing, false, or thrown authorization. A
renderer-supplied flag or credential never satisfies that callback. The host
should use a virtual-host mapping to packaged read-only content. Do not expose a
broad COM host object, DLL path, filesystem primitive, or process launcher to
the page. This repository does not ship a compiled .NET/C++ WebView2 host; the
native application must mirror both origin and session/capability checks in its
own message callback and validate them in that host's test suite.

## Packaging

### Standalone Patris installer

The existing installer remains independent. Install the executable, C ABI
library/header, pxlib and compiler-runtime DLLs together. A standalone install
does not require Electron, Tauri, WebView2, Node, or these adapters.

### Unified Digitalogic + Patris installer

Recommended layout:

```text
Digitalogic/
  Digitalogic.exe
  resources/
    app.asar
    app.asar.unpacked/native/digitalogic_patris.node
    patris/
      patris-export.exe
      patris-export.dll
      libpxlib.dll
      libgcc_s_seh-1.dll
      libstdc++-6.dll
      libwinpthread-1.dll
```

- Keep the Node-API addon and native DLLs outside ASAR.
- Resolve every native path from the signed installation root in the privileged
  process; never from renderer settings.
- Keep the executable beside its pxlib runtime so fallback remains portable.
- Preserve user configuration and databases on a normal uninstall; remove them
  only after an explicit purge choice.
- Build the Node-API addon for the exact Electron ABI and architecture.
- For Tauri, declare the executable as an external binary and native libraries
  as resources. For WebView2, install the matching architecture under a
  non-user-writable application directory.
- Standard and optional ALM builds use the same C symbols. Package a consistent
  Patris executable/DLL variant and do not mix licensed and unlicensed files.

## Validation

```powershell
npm --prefix integrations/javascript run check
npm --prefix integrations/javascript test
```

The tests cover method and origin rejection, typed errors, existing Electron
bridge reuse, DLL -> executable fallback, REST identity, absence of invented
auth headers, concurrent calls, close behavior, missing runtimes, representative
`records.list` results, and Electron/Tauri/WebView2 request routing.
