# Changelog

All notable changes to the Patris Export JavaScript host adapters are
documented in this file.

## [Unreleased]

### Added

### Changed

### Fixed

## [1.3.0] - 2026-07-26

### Added

- Added a directly usable Electron privileged-host adapter plus Tauri and
  WebView2 renderer adapters and native host-routing references, all using one
  typed result/error contract, exact origin and method allowlists,
  privileged-only native loading, existing Digitalogic Electron bridge reuse,
  DLL-to-executable-to-REST startup fallback, lifecycle/concurrency tests, and
  standalone/unified packaging guidance. Native Tauri and WebView2 host binaries
  are integration responsibilities and are not claimed by this package.
- Require a host-owned session/capability authorizer for every Electron, Tauri,
  and WebView2 request so same-origin public or login pages fail closed.
- Add an awaited Electron shutdown barrier and a self-contained sandboxed
  preload example with sanitized typed failures.
- Document and test the canonical Code-keyed `records.list` result shape and
  expose the typed public API only from the package root.
