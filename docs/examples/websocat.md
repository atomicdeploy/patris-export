# WebSocat Terminal Inspection

Use WebSocat to inspect Patris Export WebSocket messages directly from a terminal.

The WebSocket endpoint is:

```text
ws://127.0.0.1:8080/ws
```

If you run the local Windows helper, the port may be `18080`:

```text
ws://127.0.0.1:18080/ws
```

## Start The Server

```bash
patris-export serve kala.db --addr 127.0.0.1:8080 --debounce 500ms
```

## Inspect One Message

The first WebSocket message is the initial snapshot. Large databases can exceed
WebSocat's default buffer, so use a larger `-B` value.

PowerShell:

```powershell
websocat -1 -B 8388608 ws://127.0.0.1:8080/ws | python -m json.tool
```

Bash:

```bash
websocat -1 -B 8388608 ws://127.0.0.1:8080/ws | python3 -m json.tool
```

Expected top-level fields:

```json
{
  "type": "initial",
  "timestamp": "2026-07-10T07:57:52+03:30",
  "file_name": "kala.db",
  "file_path": "C:\\Patris\\data4\\kala.db",
  "total_count": 994,
  "added": []
}
```

## Watch Beautiful Summaries

PowerShell:

```powershell
.\scripts\windows\Watch-WebSocat.ps1 -Url ws://127.0.0.1:8080/ws
```

Bash:

```bash
./scripts/watch-websocket.sh ws://127.0.0.1:8080/ws
```

Example output:

```text
[2026-07-10T07:57:52+03:30] initial file=kala.db records=994 added=994 path=C:\Patris\data4\kala.db
[2026-07-10T07:58:12+03:30] update records=995 added=1 modified=0 deleted=0
[2026-07-10T07:58:14+03:30] process_info patris81=1 file_in_use=false
```

Use raw mode when you want the full JSON stream:

```powershell
.\scripts\windows\Watch-WebSocat.ps1 -Url ws://127.0.0.1:8080/ws -Raw
```

```bash
./scripts/watch-websocket.sh ws://127.0.0.1:8080/ws --raw
```

## Send A Toast Through The WebSocket

PowerShell helper:

```powershell
.\scripts\windows\Watch-WebSocat.ps1 `
  -Url ws://127.0.0.1:8080/ws `
  -ToastTitle "WebSocat check" `
  -ToastMessage "Terminal message path works"
```

Raw WebSocat:

```powershell
'{"type":"toast","title":"WebSocat check","message":"Terminal message path works","native":false,"broadcast":true}' |
  websocat -B 8388608 --max-messages=1 --max-messages-rev=2 ws://127.0.0.1:8080/ws
```

The first line is usually the initial snapshot. The second line should be the
broadcast toast message:

```json
{"type":"toast","title":"WebSocat check","message":"Terminal message path works","source":"websocket"}
```

Set `"native":true` to also try a native OS toast from the server process.

## Tips

- Use `-B 8388608` or larger for big databases.
- Use `-1` when you only want the first snapshot.
- Use `--max-messages=1 --max-messages-rev=2` when sending one stdin message and reading the initial response plus one reply.
- If your terminal cannot render Persian text, switch it to UTF-8 first.
