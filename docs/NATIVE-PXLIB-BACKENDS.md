# Native pxlib backends

Patris Export has one Paradox reader and three build-time choices for connecting
that reader to pxlib. The exported records and application APIs do not change
between modes.

| Backend | Build value | pxlib loading | Deployment trade-off |
| --- | --- | --- | --- |
| Runtime dynamic | `dynamic` | Loaded only when a `.db` file is opened | Default. The app can start without pxlib and report a normal dependency error. |
| Direct CGO | `cgo` | Linked through the platform import/shared library | Conventional C linkage. A missing runtime library can prevent process startup. |
| Static CGO | `cgo-static` | Pinned pxlib object archive is embedded | No separate pxlib DLL/shared object; platform CGO runtime dependencies can still apply. |

The backend is selected at build time. It is recorded as `native_backend` in
`BUILD-VARIANT.json` and in the Windows text build manifest.

## Windows native builds

The local helper discovers the existing AtomicDeploy Go, MinGW-w64, CMake, and
pxlib paths. It builds pxlib from the pinned commit unless `-SkipPxlibBuild` is
used.

```powershell
# Default runtime discovery; promotes to the normal deploy directory.
.\scripts\windows\Build-LocalWindows.ps1 -PxlibBackend dynamic

# Direct shared-library CGO candidate, isolated from the live deployment.
.\scripts\windows\Build-LocalWindows.ps1 -PxlibBackend cgo -SkipPromote

# Static pxlib candidate, also isolated and without libpxlib.dll.
.\scripts\windows\Build-LocalWindows.ps1 -PxlibBackend cgo-static -SkipPromote
```

Non-default variants use backend-specific staging and deployment directories so
they cannot silently overwrite the normal runtime-dynamic installation.

To reuse an existing installation, set `PXLIB_ROOT`. The shared mode requires
the pxlib import library and runtime DLL. The static mode requires
`lib\libpxlib_static.a`; the build helper creates it from CMake's pxlib object
archive.

## Linux and Windows cross-builds

```bash
./build.sh --target linux --pxlib-backend dynamic
./build.sh --target linux --pxlib-backend cgo
./build.sh --target linux --pxlib-backend cgo-static

./build.sh --target windows-cross --pxlib-backend dynamic
./build.sh --target windows-cross --pxlib-backend cgo
./build.sh --target windows-cross --pxlib-backend cgo-static
```

The equivalent Make setting is `PXLIB_BACKEND=dynamic`, `cgo`, or
`cgo-static`. When `ENABLE_ALM=1` is also selected, both the licensing and pxlib
build tags are applied.

## Runtime configuration

`PATRIS_EXPORT_PXLIB_LIBRARY`, `PATRIS_EXPORT_PXLIB_ROOT`, and `PXLIB_ROOT`
control runtime discovery only for the default `dynamic` backend. The `cgo`
backend follows the operating system's shared-library loader rules. The
`cgo-static` backend does not load pxlib at runtime.

The static option embeds pxlib, not every C runtime component. Windows packages
still include the required MinGW runtime DLLs; Linux builds remain subject to
their normal libc and CGO compatibility requirements.

The automated release remains runtime-dynamic by default. Before distributing
a static variant, review and satisfy the license terms and corresponding-source
or relinking obligations applicable to the pinned upstream pxlib revision; the
upstream source headers and packaging metadata are the authority for that
revision.

## Verification

On Windows, use the repository helper so the same compiler and headers are used
for tests and builds:

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File `
  .\scripts\windows\Invoke-CGO.ps1 -PxlibBackend dynamic go vet ./...

powershell -NoProfile -ExecutionPolicy Bypass -File `
  .\scripts\windows\Invoke-CGO.ps1 -PxlibBackend dynamic go test ./...

powershell -NoProfile -ExecutionPolicy Bypass -File `
  .\scripts\windows\Invoke-CGO.ps1 -PxlibBackend cgo go vet ./...

powershell -NoProfile -ExecutionPolicy Bypass -File `
  .\scripts\windows\Invoke-CGO.ps1 -PxlibBackend cgo go test ./...

powershell -NoProfile -ExecutionPolicy Bypass -File `
  .\scripts\windows\Invoke-CGO.ps1 -PxlibBackend cgo-static go vet ./...

powershell -NoProfile -ExecutionPolicy Bypass -File `
  .\scripts\windows\Invoke-CGO.ps1 -PxlibBackend cgo-static go test ./...

.\scripts\windows\Build-LocalWindows.ps1 -PxlibBackend cgo -SkipPromote

.\scripts\windows\Build-LocalWindows.ps1 -PxlibBackend cgo-static -SkipPromote
```

The `pkg/paradox` test suite opens the checked-in real `testdata/kala.db`
fixture. A tagged build with `CGO_ENABLED=0` fails immediately with an explicit
`pxlib_cgo_requires_CGO_ENABLED_1` compiler diagnostic instead of silently
falling back to another backend. The secondary `pxlib_cgo_static` tag likewise
fails unless `pxlib_cgo` is present, so a misspelled direct build cannot produce
a runtime-dynamic artifact mislabeled as static.
