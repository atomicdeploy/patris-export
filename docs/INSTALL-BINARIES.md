# Installing release binaries

GitHub Releases are built from the exact tagged source commit after the Go,
web, Linux, native Windows, and Windows cross-build checks pass. Verify the
download with the release's `SHA256SUMS` before installation.

## Windows amd64

For a normal desktop installation, download
`patris-export-vX.Y.Z-windows-amd64-setup.exe`. The assisted installer provides
current-user and all-users modes, start-menu and optional desktop shortcuts,
an optional C SDK component, registered uninstall support, and safe upgrades.
Configuration is preserved by default; removal requires an explicit checkbox
or the documented `/PURGEDATA` silent-uninstall switch. See
[WINDOWS_INSTALLER.md](WINDOWS_INSTALLER.md) for deployment and build details.

For a portable or embedding-oriented installation:

1. Download `patris-export-vX.Y.Z-windows-amd64.zip`.
2. Extract the complete archive to a permanent directory such as
   `C:\Program Files\Patris Export`.
3. Keep `libpxlib.dll` beside `patris-export.exe` for Paradox `.db` reading.
   The executable still starts without pxlib and reports a friendly
   native-runtime error when a `.db` read is attempted.
4. Run `patris-export.exe --version`, then smoke-test a database:

   ```powershell
   .\patris-export.exe info C:\Patris\data4\kala.db
   ```

The bundle also contains `patris-export.dll` and `patris-export.h` for
embedding. Existing configuration remains external to the install directory
and is not overwritten by extracting a newer release.

## Linux amd64

1. Download `patris-export-vX.Y.Z-linux-amd64.tar.gz`.
2. Extract it and run the included launcher, which loads the bundled pxlib
   runtime from the same directory:

   ```bash
   tar -xzf patris-export-vX.Y.Z-linux-amd64.tar.gz
   cd patris-export-linux-amd64
   ./run-patris-export.sh --version
   ./run-patris-export.sh info /path/to/kala.db
   ```

To make it system-wide, keep the extracted directory intact and symlink the
launcher into a directory on `PATH`. The bundle also contains
`libpatris-export.so` and `libpatris-export.h` for embedding.

For custom runtime placement, set `PATRIS_EXPORT_PXLIB_LIBRARY` to an exact
DLL/shared-object path, or set `PATRIS_EXPORT_PXLIB_ROOT` to a prefix containing
`bin` or `lib`. Linux falls back to the normal dynamic linker search path after
checking the executable directory and configured roots.

## Integrity verification

On Linux or Git Bash:

```bash
grep 'linux-amd64.tar.gz$' SHA256SUMS | sha256sum -c -
```

On Windows PowerShell:

```powershell
Get-FileHash .\patris-export-vX.Y.Z-windows-amd64.zip -Algorithm SHA256
```

Compare the PowerShell result with the matching line in `SHA256SUMS`.
Download both platform archives before using `sha256sum -c SHA256SUMS` to
verify the complete release set in one command.
