# Large-table rendering and measurement

The live viewer keeps one semantic HTML table and one row-construction path.
When the active result contains more than 200 rows, the body mounts the visible
viewport, eight overscan rows on each side, and only the logical rows that must
remain mounted for focus, scroll-anchor restoration, or changed-row navigation.
Spacer rows preserve the full scroll range.

The table exposes the complete logical size through `aria-rowcount`, each
mounted record exposes its absolute `aria-rowindex`, and the Code-keyed roving
focus list still contains every logical row. Selection, sorting, column resize,
context menus, RTL/LTR presentation, and inspector actions therefore continue
to use the existing table implementation rather than a parallel compact table.

## Deterministic regression fixture

`web/test/fixtures/table-performance-1002.mjs` creates exactly 1,002 records.
Every run uses the same codes, Persian/English labels, nested warehouse maps,
arrays, nulls, zeroes, and booleans. Run the regression with:

```powershell
cd web
npm.cmd test
```

The test positions a 600-pixel viewport around logical row 501 and pins the
first and last rows. The mounted-row budget is at most 30 rows, including the
two pinned rows. It also proves that spacer rows plus mounted intervals account
for all 1,002 logical rows and that structured values never render as
`[object Object]`.

## Browser measurement

After opening a dataset with at least 1,002 rows, select the table in browser
developer tools and inspect these attributes:

- `data-total-rows`: logical rows in the current result.
- `data-rendered-rows`: actual mounted record rows.
- `data-render-duration-ms`: duration of the latest body render.
- `data-virtualized`: whether windowing is active.

For a repeatable manual measurement, use a 1,280 x 800 browser viewport, keep
pagination disabled, scroll to approximately the middle, and record the four
attributes plus `document.querySelectorAll('#tableBody tr[data-row-key]').length`.
The mounted-row count must remain bounded as the logical row count grows. Use
the browser Performance panel for timing comparisons; take five reload samples
and report the median so extensions or a cold JavaScript engine do not dominate
one sample.
