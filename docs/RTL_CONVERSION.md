# RTL Conversion

Patris Export includes an experimental opt-in RTL conversion path for mixed
Persian/Latin text.

The default output remains unchanged. Enable this only when the consumer expects
logical RTL ordering instead of the legacy visual ordering from Patris81.

## Backend Conversion

Use the CLI flag:

```bash
patris-export convert kala.db --rtl -o exports
patris-export serve kala.db --rtl --addr 127.0.0.1:8080
```

Or use configuration/environment:

```json
{
  "database": {
    "rtl_conversion": true
  }
}
```

```bash
PATRIS_EXPORT_RTL=1 patris-export serve kala.db
```

## Web Display Direction

The web viewer also has a settings checkbox named "Display table text
right-to-left". This is display-only; it changes table text direction in the
browser without changing backend conversion output.

## Current Scope

This is intentionally conservative for now. It handles common mixed
Persian/Latin text such as:

| Visual input | RTL logical output |
| --- | --- |
| `LAN8720 ماژول شبکه` | `ماژول شبکه LAN8720` |

Pure Persian text with embedded numbers is intentionally left unchanged in this
pass. Cases such as `لیزر میلی وات ولت 5 قرمز 5 نقطه` need a more careful
domain-specific conversion pass and remain future work.
