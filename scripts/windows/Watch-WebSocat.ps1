param(
    [string]$Url = "ws://127.0.0.1:8080/ws",
    [int]$BufferSize = 8388608,
    [switch]$Raw,
    [switch]$Once,
    [string]$ToastTitle,
    [string]$ToastMessage,
    [switch]$NativeToast
)

$ErrorActionPreference = "Stop"

$websocat = Get-Command websocat -ErrorAction SilentlyContinue | Select-Object -First 1
if (-not $websocat) {
    throw "websocat was not found in PATH."
}

function Format-WebSocketMessage {
    param([string]$Line)

    if ($Raw) {
        return $Line
    }

    try {
        $message = $Line | ConvertFrom-Json
    } catch {
        return $Line
    }

    $timestamp = if ($message.timestamp) { $message.timestamp } else { (Get-Date).ToString("s") }

    switch ($message.type) {
        "initial" {
            $added = if ($message.added) { @($message.added).Count } else { 0 }
            return "[$timestamp] initial file=$($message.file_name) records=$($message.total_count) added=$added path=$($message.file_path)"
        }
        "update" {
            $added = if ($message.added) { @($message.added).Count } else { 0 }
            $modified = if ($message.modified) { @($message.modified).Count } else { 0 }
            $deleted = if ($message.deleted) { @($message.deleted).Count } else { 0 }
            return "[$timestamp] update records=$($message.total_count) added=$added modified=$modified deleted=$deleted"
        }
        "toast" {
            $native = if ($message.native_error) { " native_error=$($message.native_error)" } else { "" }
            return "[$timestamp] toast title=`"$($message.title)`" message=`"$($message.message)`" source=$($message.source)$native"
        }
        "process_info" {
            $status = if ($message.status) { $message.status } else { $message }
            return "[$timestamp] process_info patris81=$($status.patris81.count) file_in_use=$($status.file_access.in_use)"
        }
        "config_update" {
            return "[$timestamp] config_update schema=$($message.config.schema_version)"
        }
        default {
            return "[$timestamp] $($message.type): $Line"
        }
    }
}

$argsList = @("-B", $BufferSize.ToString())
if ($Once -and -not $ToastMessage) {
    $argsList += "-1"
}
if ($ToastMessage) {
    $argsList += @("--max-messages=1", "--max-messages-rev=2")
}
$argsList += $Url

if ($ToastMessage) {
    $title = if ($ToastTitle) { $ToastTitle } else { "WebSocat" }
    $payload = @{
        type = "toast"
        title = $title
        message = $ToastMessage
        native = [bool]$NativeToast
        broadcast = $true
    } | ConvertTo-Json -Compress

    $payload | & $websocat.Source @argsList | ForEach-Object {
        Format-WebSocketMessage $_
    }
} else {
    & $websocat.Source @argsList | ForEach-Object {
        Format-WebSocketMessage $_
    }
}
