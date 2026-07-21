# pxlib FFI boundary

Patris Export loads the pinned pxlib revision in `dependencies/pxlib.ref` at
runtime by default. Library and symbol handles are integer-valued platform
handles, but pxlib data is always represented as `unsafe.Pointer` or a typed
native pointer. Data pointers must never make a `pointer -> uintptr -> pointer`
round trip.

## Audited ABI

The definitions below mirror `paradox.h` at pxlib revision
`e32d17611e5ee353c4e3ce04e61b0b38feb95855`:

```c
struct px_field { char *px_fname; char px_ftype; int px_flen; int px_fdc; };
struct px_val {
    char isnull;
    int type;
    union { long lval; double dval; struct { char *val; int len; } str; } value;
};
```

The release targets are Windows amd64 (LLP64) and Linux amd64 (LP64). Both use
8-byte pointers and 4-byte `int`; `px_field` has offsets 0/8/12/16 and size 24,
while `px_val` has offsets 0/4/8 and size 24. Windows uses a 4-byte C `long`
on both ILP32 and LLP64. Unix uses a 4-byte C `long` on ILP32 and an 8-byte C
`long` on LP64. The platform-specific `pxLong` readers select that width before
sign-extending to Go's `int64`. The string union member is 16 bytes on both
64-bit targets.

The Go mirrors are layout-correct for ILP32 compilation: 4-byte pointers
produce a 16-byte `px_field`, a 16-byte `px_val`, and an 8-byte string member.
Patris Export does not publish any 32-bit artifact. Unit tests derive and
assert all offsets and sizes from the active pointer width, so a new
architecture fails before it can silently use a different layout.

CI runs the negative-C-long regression as a Linux/386 executable; this would
decode `-1` as `4294967295` if an ILP32 build read the full 8-byte union.
Linux/386 is the only ILP32 runtime exercised by this audit. Windows/386
runtime-dynamic remains unsupported and unverified: pxlib's WIN32 API uses
`__cdecl`, and cross-compiling cannot prove that purego `RegisterFunc` invokes
that 32-bit calling convention correctly. The Windows/386 checks in this work
are layout/compilation evidence only, not a runtime support claim.

## Bounds, ownership, and lifetime

- `PX_get_num_fields` is validated as non-negative and no greater than the
  unsigned 16-bit Paradox header limit before it sizes the `pxval_t **` view.
  A non-empty record with a null array pointer is rejected.
- The pinned pxlib parser uses a 300-byte temporary field-name buffer and
  always appends a NUL. Patris Export scans with `unsafe.Add` and fails if a
  terminator is not found within that exact contract; it never performs an
  unbounded integer-address walk.
- String/blob/byte values are copied immediately using pxlib's signed
  32-bit length. Negative lengths and a null pointer paired with a positive
  length are rejected before `unsafe.Slice` is constructed.
- `PX_get_field` returns metadata owned by the open `pxdoc_t`.
  `PX_retrieve_record` returns an array and values allocated through the
  document's configured allocator. The pinned public API exposes no matching
  cross-runtime release function. Patris Export therefore copies every field
  synchronously, retains no native pointer, and does not call a potentially
  incompatible C runtime's `free`.
- Each `Database` holds a read lock for the complete metadata/value-copy
  window. `Close` takes the write lock before `PX_close` and `PX_delete`, so
  document-owned memory cannot be released concurrently with a Go read.
  `runtime.KeepAlive` makes the access window explicit to the compiler.

## Backends

The default `runtime-dynamic` backend binds the typed signatures through
purego. Optional `cgo` and `cgo-static` variants must implement the same
`pxlib` function fields by converting `C.pxdoc_t *`, `C.pxfield_t *`, and
`C.pxval_t **` directly to `unsafe.Pointer` or their corresponding typed Go
pointers. They must not restore integer-held data addresses. Link mode does not
change the C layouts, ownership, bounds, or locking rules above.

## Verification

The portable regression gate is:

```text
go vet ./...
go test ./...
go test -race ./pkg/paradox
```

Release workflows additionally build and read `testdata/kala.db` with the
source-built pxlib runtime on Linux amd64, native Windows amd64, and the MinGW
Windows cross-build under Wine. Optional CGO link-mode verification remains
part of the backend-specific work and must use the same real-database smoke
test before such an artifact is published.
