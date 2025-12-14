# RTL Text Conversion Example

This example demonstrates the RTL text conversion feature that converts text from LTR-visual order to RTL-logical order.

## Problem Statement

The Patris81 database stores mixed Persian/English text in visual LTR order. For example:
- Stored as: `"LAN8720 ماژول شبکه"`
- Displays correctly in LTR contexts

However, when displaying in RTL contexts (like RTL-enabled UIs), the text needs to be in logical RTL order:
- Converted to: `"ماژول شبکه LAN8720"`
- Displays correctly in RTL contexts

## Solution

The `ConvertLTRVisualToRTL()` function performs this conversion by:

1. **Segmenting**: Dividing text into word/token segments
2. **Identifying**: Detecting whether each segment is RTL (Persian/Arabic) or LTR (Latin/numbers)
3. **Reversing**: Reversing the order of segments while keeping each segment's internal order intact

## Usage

### Command Line

Enable RTL conversion with the `--rtl` or `-r` flag:

```bash
# Convert database with RTL optimization
patris-export convert kala.db --rtl -o output/

# With character mapping
patris-export convert kala.db -c testdata/farsi_chars.txt --rtl -o output/

# Start server with RTL conversion
patris-export serve kala.db --rtl -a :8080
```

### Programmatic Usage

```go
import "github.com/atomicdeploy/patris-export/pkg/converter"

// Enable RTL conversion globally
converter.SetRTLConversion(true)

// Convert Patris-encoded text
result := converter.Patris2Fa(patrisEncodedText)
// Result will be in RTL-logical order

// Or convert already-decoded text
text := "LAN8720 ماژول شبکه"
rtlText := converter.ConvertLTRVisualToRTL(text)
// rtlText = "ماژول شبکه LAN8720"
```

## Examples

| Input (LTR Visual) | Output (RTL Logical) |
|-------------------|---------------------|
| `LAN8720 ماژول شبکه` | `ماژول شبکه LAN8720` |
| `ARDUINO با` | `با ARDUINO` |
| `BLUE PILL STM32F103C8T6 ماژول` | `ماژول STM32F103C8T6 PILL BLUE` |
| `ماژول` | `ماژول` (unchanged - pure Persian) |
| `LAN8720` | `LAN8720` (unchanged - pure English) |

## Technical Details

### Character Detection

The function uses Unicode ranges to identify Persian/Arabic characters:
- `0x0600-0x06FF`: Arabic
- `0xFB50-0xFDFF`: Arabic Presentation Forms-A
- `0xFE70-0xFEFF`: Arabic Presentation Forms-B

### Segment Reversal

The algorithm preserves:
- ✅ Word boundaries (spaces)
- ✅ Internal word order
- ✅ Character order within each word

Only the order of word segments is reversed.

## When to Use RTL Conversion

Use the `--rtl` flag when:
- ✅ Your UI displays text in RTL mode
- ✅ You have mixed Persian/English content
- ✅ Text needs to read naturally in RTL contexts

Do NOT use when:
- ❌ Your UI displays text in LTR mode (default behavior is correct)
- ❌ You only have pure Persian or pure English text (conversion has no effect)
- ❌ You need the original visual order preserved
