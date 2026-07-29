# TUI guide

The terminal user interface is an operations dashboard for the same source and
configuration used by the CLI and server. It is useful over SSH, in a service
terminal, or when a browser is unnecessary.

## Start

```bash
patris-export tui /path/to/database.db
```

With no argument, the TUI uses `database.path`:

```bash
patris-export --config ./config/patris-export.yaml tui
```

The dashboard refreshes source and process state every three seconds. Press
`r` for an immediate refresh.

## Tabs

| Number | Tab | Content |
| --- | --- | --- |
| `1` | Dashboard | Source path/state, size, timestamps, row counts, Patris81 process state, and API shortcuts. |
| `2` | Data | Detected transformed fields with type/use hints. |
| `3` | Config | Active config path, source, bind address, watch/temp/UI settings, and direct-access state. |
| `4` | Charmap | Embedded or custom character-map entries and ignored-line count. |
| `5` | Processes | Patris81 processes and database file holders when the platform can inspect them. |
| `6` | Tools | Viewer, WebSocket, source-file, and update links. |
| `7` | About | Version, commit, build date, Go version, platform, and TUI capabilities. |

The summary classifies familiar `kala.db` rows by code depth and reports
positive/zero stock, but the TUI can inspect any readable table. It is not a
replacement for `/api/products` when a consumer needs the full canonical
product projection.

## Keyboard

| Key | Action |
| --- | --- |
| `Tab`, `Right`, `l` | Next tab |
| `Shift+Tab`, `Left`, `h` | Previous tab |
| `1` through `7` | Jump to a tab |
| `Up`, `k` | Scroll up |
| `Down`, `j` | Scroll down |
| `PgUp`, `PgDown` | Scroll by half a content page |
| `Home` | Return to the beginning |
| `r` | Refresh immediately |
| `o` | Open the configured web viewer |
| `w` | Launch the WebSocat helper against the configured WebSocket |
| `q`, `Esc`, `Ctrl+C` | Quit |

## What the TUI reads

The TUI creates a data source using the configured temp-copy/direct-access
policy, reads records, and inspects:

- source existence, size, and modified time;
- transformed field names and row counts;
- running `Patris81.exe` processes;
- processes holding the local database file, where supported;
- the embedded or selected character map;
- HTTP/WebSocket/source-file URLs derived from `server.host` and
  `server.port`.

The dashboard is read-only except for launching viewer/WebSocat processes. It
does not start the HTTP server itself. Start `patris-export serve` separately
before relying on the displayed URLs.

## Remote sources

An HTTP(S) database URL can be configured, but process and local file-holder
inspection applies only to local files. A remote source is downloaded through
the normal datasource/temp policy when records are read.

## WebSocat helper

The `w` key launches:

- `scripts\windows\Watch-WebSocat.ps1` in a new PowerShell window on Windows;
- `scripts/watch-websocket.sh` through a terminal emulator on other supported
  desktops.

Install WebSocat first and see
[the terminal WebSocket example](examples/websocat.md). If the desktop cannot
launch a new terminal, copy the displayed WebSocket URL and run WebSocat
manually.

## Troubleshooting

- **Read error:** run `patris-export info <file>` to separate a pxlib/source
  error from a TUI rendering issue.
- **Viewer key does nothing:** verify a default browser exists and that the
  server is running at the configured address.
- **File holder inspection unavailable:** this is platform-dependent and does
  not mean the file is safe for direct access.
- **Layout is clipped:** enlarge the terminal. The TUI keeps a minimum logical
  width, but the terminal still controls visible cells and Persian glyph
  shaping.
- **Stale configuration:** exit and restart after changing a config file. The
  TUI reads its effective config at startup; its periodic tick refreshes data
  and process state, not the entire configuration hierarchy.
