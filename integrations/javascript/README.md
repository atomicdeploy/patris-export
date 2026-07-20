# Patris Export JavaScript host adapters

These CommonJS adapters define one Patris Export renderer contract for Electron,
Tauri, and WebView2 without moving database, transformation, pricing, or export
logic into JavaScript. Electron has a directly usable privileged-host adapter;
Tauri and WebView2 have renderer adapters plus framework-neutral host-routing
references that native applications must bind in their privileged layer.
`cmd/patris-export-lib` remains the only native ABI and the normal Patris
executable and REST API remain the fallbacks.

See the adapter-specific [`CHANGELOG.md`](CHANGELOG.md) for release notes.

The security boundary is deliberate:

```text
untrusted renderer
  -> typed allowlisted IPC/message call
  -> privileged Electron/Tauri/WebView2 host (validates its own source URL)
  -> Patris DLL worker
  -> Patris executable on loopback
  -> configured Patris REST endpoint
```

No adapter creates or transfers an authentication token. Application login is
the host application's responsibility. The Patris bridge is an integration
transport, not a second login system.

For Digitalogic, keep the existing WordPress session and capability checks on
the canonical `/panel/` route. Do not exchange that browser session for a bearer
token or forward a renderer-supplied credential through this bridge.

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
  "result": [{ "code": "1001", "name": "Example module" }],
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
const { createConfiguredHost } = require('./src/index.cjs');

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
const { PrivilegedPatrisHost, wrapExistingElectronBridge } = require('./src');

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
const { ipcMain } = require('electron');
const { registerElectronPatrisHost } = require('./src/electron.cjs');

const unregister = registerElectronPatrisHost({ ipcMain, host });
app.once('before-quit', async () => {
  unregister();
  await host.close();
});
```

Use [`examples/electron-preload.cjs`](examples/electron-preload.cjs) with
`contextIsolation: true`, `sandbox: true`, and `nodeIntegration: false`.
Electron origin checks use `event.senderFrame.url`; a URL sent inside the
renderer payload is ignored. Prefer a registered `app://digitalogic` scheme for
packaged local content instead of allowing the broad `file://` origin.

## Tauri

Bundle [`src/tauri.cjs`](src/tauri.cjs) into the web frontend and supply Tauri's
`invoke` function:

```js
const { invoke } = require('@tauri-apps/api/core');
const { createTauriRendererClient } = require('./src/tauri.cjs');
const patris = createTauriRendererClient({ invoke });
const response = await patris.call('records.list');
```

Implement two privileged commands, `patris_invoke` and `patris_status`. They
must derive the caller URL/window label from Tauri's command context, compare it
with an exact allowlist, and then call the same host contract. Never accept an
origin, path, native handle, or endpoint from the command payload. In a native
Tauri build, load `patris-export.dll`/`libpatris-export.so` from the signed
application resource directory or launch the Patris sidecar; JavaScript must
not call `libloading`, FFI plugins, or shell commands directly.

The JavaScript renderer adapter and framework-neutral command-routing reference
are covered by the Node test suite. This repository does not ship a compiled
Tauri/Rust command plugin, and this workstation currently has `rustup` but no
installed Rust toolchain, so no native Tauri build is claimed. A production
Tauri application must implement and test the two commands in its privileged
Rust core (or a signed sidecar) before calling that integration native-ready.
This does not affect Electron, the C ABI, or the Patris executable fallback.

## WebView2

Bundle [`src/webview2.cjs`](src/webview2.cjs) into trusted app content:

```js
const { createWebView2RendererClient } = require('./src/webview2.cjs');
const patris = createWebView2RendererClient({ webview: window.chrome.webview });
const response = await patris.call('records.list');
```

The native .NET/C++ host listens for `patris:request`, reads the source from the
WebView2 event/navigation state, checks an exact origin such as
`https://patris.local`, and answers with `patris:result`. The host should use a
virtual-host mapping to packaged read-only content. Do not expose a broad COM
host object, DLL path, filesystem primitive, or process launcher to the page.
`createWebView2MessageHandler` is the JavaScript reference harness used by the
tests. This repository does not ship a compiled .NET/C++ WebView2 host; the
native application must mirror this routing and origin policy in its own message
callback and validate it in that host's test suite.

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
