# Changelog

All notable changes to the Patris Export JavaScript host adapters are
documented in this file.

## [Unreleased]

### Added

- Added a directly usable Electron privileged-host adapter plus Tauri and
  WebView2 renderer adapters and native host-routing references, all using one
  typed result/error contract, exact origin and method allowlists,
  privileged-only native loading, existing Digitalogic Electron bridge reuse,
  DLL-to-executable-to-REST startup fallback, lifecycle/concurrency tests, and
  standalone/unified packaging guidance. Native Tauri and WebView2 host binaries
  are integration responsibilities and are not claimed by this package.
