#!/usr/bin/env bash
set -euo pipefail

URL="ws://127.0.0.1:8080/ws"
RAW=0
ONCE=0
TOAST_TITLE=""
TOAST_MESSAGE=""
NATIVE=false
BUFFER_SIZE="${WEBSOCAT_BUFFER_SIZE:-8388608}"

if [ "${1:-}" != "" ] && [[ "${1:-}" != --* ]]; then
    URL="$1"
    shift
fi

while [ "$#" -gt 0 ]; do
    case "$1" in
        --raw) RAW=1 ;;
        --once) ONCE=1 ;;
        --toast-title) TOAST_TITLE="${2:?--toast-title requires a value}"; shift ;;
        --toast-message) TOAST_MESSAGE="${2:?--toast-message requires a value}"; shift ;;
        --native) NATIVE=true ;;
        -h|--help)
            echo "Usage: $0 [ws://host:port/ws] [--once] [--raw] [--toast-title TITLE --toast-message MESSAGE] [--native]"
            exit 0
            ;;
        *) echo "Unknown option: $1" >&2; exit 2 ;;
    esac
    shift
done

if ! command -v websocat >/dev/null 2>&1; then
    echo "websocat was not found in PATH." >&2
    exit 1
fi

if command -v python3 >/dev/null 2>&1; then
    PYTHON=python3
elif command -v python >/dev/null 2>&1; then
    PYTHON=python
else
    echo "python3/python was not found in PATH." >&2
    exit 1
fi

format_stream() {
    "$PYTHON" -u -c '
import json, sys
raw = sys.argv[1] == "1"
for line in sys.stdin:
    line = line.rstrip("\n")
    if raw:
        print(line, flush=True); continue
    try:
        message = json.loads(line)
    except json.JSONDecodeError:
        print(line, flush=True); continue
    timestamp = message.get("timestamp", "")
    msg_type = message.get("type", "message")
    if msg_type == "initial":
        file_name = message.get("file_name")
        total = message.get("total_count")
        added = len(message.get("added") or [])
        file_path = message.get("file_path")
        print(f"[{timestamp}] initial file={file_name} records={total} added={added} path={file_path}", flush=True)
    elif msg_type == "update":
        total = message.get("total_count")
        added = len(message.get("added") or [])
        modified = len(message.get("modified") or [])
        deleted = len(message.get("deleted") or [])
        print(f"[{timestamp}] update records={total} added={added} modified={modified} deleted={deleted}", flush=True)
    elif msg_type == "toast":
        suffix = f" native_error={message.get('native_error')}" if message.get("native_error") else ""
        title = message.get("title")
        body = message.get("message")
        source = message.get("source")
        print(f"[{timestamp}] toast title=\"{title}\" message=\"{body}\" source={source}{suffix}", flush=True)
    elif msg_type == "process_info":
        status = message.get("status") or message
        patris = status.get("patris81") or {}
        file_access = status.get("file_access") or {}
        print(f"[{timestamp}] process_info patris81={patris.get('count')} file_in_use={file_access.get('in_use')}", flush=True)
    elif msg_type == "config_update":
        config = message.get("config") or {}
        print(f"[{timestamp}] config_update schema={config.get('schema_version')}", flush=True)
    else:
        print(f"[{timestamp}] {msg_type}: {line}", flush=True)
' "$RAW"
}

args=(-B "$BUFFER_SIZE")
if [ "$ONCE" = "1" ] && [ -z "$TOAST_MESSAGE" ]; then
    args+=(-1)
fi
if [ -n "$TOAST_MESSAGE" ]; then
    args+=(--max-messages=1 --max-messages-rev=2)
    payload="$("$PYTHON" -c 'import json,sys; print(json.dumps({"type":"toast","title":sys.argv[1] or "WebSocat","message":sys.argv[2],"native":sys.argv[3]=="true","broadcast":True}, ensure_ascii=False))' "$TOAST_TITLE" "$TOAST_MESSAGE" "$NATIVE")"
    printf "%s\n" "$payload" | websocat "${args[@]}" "$URL" | format_stream
else
    websocat "${args[@]}" "$URL" | format_stream
fi
