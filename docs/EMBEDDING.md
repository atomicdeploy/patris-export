# Embedding Patris Export

Patris Export can run in three modes:

- Standalone mode: the normal `patris-export serve ...` executable with Web UI, REST API, and WebSocket.
- Embedded Go mode: import `github.com/atomicdeploy/patris-export/pkg/embedded` and call the engine directly from a host application.
- Loadable library mode: build `patris-export.dll` on Windows or `libpatris-export.so` on Linux and call the C ABI from C#, Electron native modules, Python `ctypes`, or another host.

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

The library still uses CGO and pxlib. Keep `pxlib.dll` beside the Windows host executable or make it available through `PATH`. On Linux, keep `libpx.so` in the system linker path or set `LD_LIBRARY_PATH`.

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
- `PatrisExportCreate(char* options_json) uint64_t`
- `PatrisExportClose(uint64_t handle) int`
- `PatrisExportCall(uint64_t handle, char* request_json) char*`
- `PatrisExportStartHTTP(uint64_t handle, char* addr) int`
- `PatrisExportStartIPC(uint64_t handle, char* path) char*`
- `PatrisExportLastError() char*`
- `PatrisExportFreeString(char* value)`

Every returned string must be released with `PatrisExportFreeString`.

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
