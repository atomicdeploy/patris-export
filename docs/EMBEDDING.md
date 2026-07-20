# Embedding Patris Export

Patris Export can run in three modes:

- Standalone mode: the normal `patris-export serve ...` executable with Web UI, REST API, and WebSocket.
- Embedded Go mode: import `github.com/atomicdeploy/patris-export/pkg/embedded` and call the engine directly from a host application.
- Loadable library mode: build `patris-export.dll` on Windows or `libpatris-export.so` on Linux and call the C ABI from C#, Electron native modules, Python `ctypes`, or another host.

Supported Electron, Tauri, and WebView2 renderer/host contracts, the typed
method allowlist, security boundary, runtime fallback order, tests, and
standalone/unified installer layout are documented in
[`integrations/javascript/README.md`](../integrations/javascript/README.md).
Those adapters call this ABI or the existing executable/REST transports; they
do not implement a parallel transformation engine.

The embedded and IPC APIs use the same backend as the standalone server, so records, config, process/file-lock status, notifications, and live update events stay consistent.

## Build The Loadable Library

Linux:

```bash
make build-lib-linux
```

Windows:

```bash
make build-lib-windows
```

Outputs:

- Windows: `build/patris-export.dll` and `build/patris-export.h`
- Linux: `build/libpatris-export.so` and `build/libpatris-export.h`

By default, the library loads pxlib at runtime when `.db` data is read. Keep `libpxlib.dll`
beside the Windows host executable, set `PATRIS_EXPORT_PXLIB_LIBRARY` to an
exact runtime path, or set `PATRIS_EXPORT_PXLIB_ROOT` to a prefix containing
`bin` or `lib`. On Linux, keep `libpx.so`/`libpxlib.so` beside the host binary,
under `PATRIS_EXPORT_PXLIB_ROOT`, or in the normal dynamic linker search path.
Missing pxlib is returned as a normal API error instead of aborting the host
process at load time.

Hosts that prefer a normal CGO link can build with `PXLIB_BACKEND=cgo`; hosts
that must not deploy a separate pxlib DLL/shared object can use
`PXLIB_BACKEND=cgo-static`. These choices change only how pxlib is linked, not
the C ABI exported by Patris Export. See
[native pxlib backend choices](NATIVE-PXLIB-BACKENDS.md).

## Go Embedded API

```go
engine, err := embedded.New(embedded.Options{
    DatabasePath: `C:\Patris\data4\kala.db`,
    Watch:        true,
    WatchSet:     true,
    Debounce:     "500ms",
})
if err != nil {
    panic(err)
}
defer engine.Close()

records, err := engine.Server().Records()
status := engine.Server().Status()
```

To mount the existing REST/WebSocket/Web UI routes inside a larger Go server:

```go
http.Handle("/patris/", http.StripPrefix("/patris", engine.Server().Router()))
```

## C ABI

The exported C functions are:

- `PatrisExportVersionJSON() char*`
- `PatrisExportABIVersion() uint32_t`
- `PatrisExportCapabilitiesJSON() char*`
- `PatrisExportLicenseStatusJSON() char*`
- `PatrisExportLicenseChallenge() char*`
- `PatrisExportLicenseInstall(char* key) char*`
- `PatrisExportLicenseRemove() char*`
- `PatrisExportCreate(char* options_json) uint64_t`
- `PatrisExportClose(uint64_t handle) int`
- `PatrisExportCall(uint64_t handle, char* request_json) char*`
- `PatrisExportStartHTTP(uint64_t handle, char* addr) int`
- `PatrisExportStartIPC(uint64_t handle, char* path) char*`
- `PatrisExportLastError() char*`
- `PatrisExportFreeString(char* value)`

Every returned string must be released with `PatrisExportFreeString`.

ABI version 1 serializes operations for each engine handle. Closing a handle
removes it from the registry immediately, rejects new calls, waits for an
already-running call, and then closes the engine. Successful ABI operations
clear the previous error. `PatrisExportLastError` is a process-global snapshot,
so a caller should copy and free it immediately after a failed operation rather
than relying on it across concurrent calls.

The capabilities document lists the canonical direct/IPC request methods,
transport support, string ownership, threading contract, and build-time
licensing mode. Runtime converter
and temporary-file settings are currently process-wide, so hosts should use one
active engine per process. All C entry points contain Go panics at the ABI
boundary and report a contained panic through `PatrisExportLastError` where the
platform runtime permits recovery.

License management symbols are present in every build, preserving one host ABI
for standard and optional licensed variants. Standard builds report that no
license is required. Builds created with the `alm_compat` tag fail
`PatrisExportCreate` until a valid per-user or legacy adjacent key is found;
hosts can query the status/challenge and install or remove the per-user key
without creating an engine. See [LICENSING.md](LICENSING.md) for the exact
profile, build flags, key locations, attribution, and security limitations.

Example options JSON:

```json
{
  "database_path": "C:\\Patris\\data4\\kala.db",
  "watch": true,
  "watch_set": true,
  "debounce": "500ms"
}
```

Example request JSON:

```json
{"id":1,"method":"records.list"}
```

## Local IPC

IPC uses newline-delimited JSON requests and responses. It is useful when the host application does not want to expose a TCP/HTTP port.

Default endpoints:

- Windows named pipe: `\\.\pipe\patris-export`
- Linux/macOS Unix socket: `/tmp/patris-export.sock`

Start standalone with IPC only:

```bash
patris-export serve C:\Patris\data4\kala.db --ipc --http=false
```

Start standalone with both HTTP and IPC:

```bash
patris-export serve C:\Patris\data4\kala.db --addr 127.0.0.1:18080 --ipc
```

Call IPC from the bundled client:

```bash
patris-export ipc app.get
patris-export ipc records.list
patris-export ipc status.get
patris-export ipc toast.show "{\"title\":\"Patris Export\",\"message\":\"Hello from IPC\",\"native\":true,\"broadcast\":true}"
```

Subscribe to live events:

```bash
patris-export ipc subscribe
```

Supported methods:

- `app.get`
- `records.list`
- `info.get`
- `status.get`
- `config.get`
- `config.set`
- `toast.show`
- `refresh`
- `subscribe`

Each request is one JSON object per line:

```json
{"id":1,"method":"status.get"}
```

A normal response looks like:

```json
{"id":1,"ok":true,"result":{...}}
```

After `subscribe`, the connection stays open and event messages look like:

```json
{"type":"event","event":{"type":"update","timestamp":"2026-07-10T12:00:00Z"}}
```
