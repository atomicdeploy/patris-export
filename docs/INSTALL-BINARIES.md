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

### Task Scheduler and environment-backed credentials

Windows Task Scheduler does not refresh an already-created task process from
the current user's environment after a credential is added or rotated. Use the
bundled installer helper to name each environment variable that Patris must
re-read at launch:

```powershell
.\Install-PatrisExportScheduledTask.ps1 `
  -Action Install `
  -DbPath C:\Patris\data4\kala.db `
  -Address 127.0.0.1:18080 `
  -ImportEnvironmentVariable DIGITALOGIC_PRICING_INPUT_TOKEN `
  -ExtraArgs @("--raw=false", "--watch=false") `
  -Start
```

The task stores only the validated variable name. Its launcher reads the
current user-scoped value, falls back to the machine scope, imports it into the
child process, and fails closed if no non-empty value exists. The value is
never written to the task XML, command line, Patris config, or launcher output.
The helper uses the co-located `patris-export.exe` in a ZIP or installer
deployment. Repository operators retain the existing
`%USERPROFILE%\Desktop\AtomicDeploy\deploy` fallback and can select another
location with `-DeploymentDirectory`.
Re-run the install action when task arguments or imported variable names
change; rotating the value itself requires only a task restart.
The launcher records only its process identity (PID, creation time, and exact
executable path) under the current user's local application-data directory.
The helper uses that non-secret record to stop, restart, upgrade, or uninstall
only the scheduled server child; an unrelated viewer or CLI process from the
same executable remains untouched. Launch reservations and an exclusive
deployment lock prevent Task Scheduler retries from creating untracked
duplicate children.
The assisted uninstaller stops and removes the default task only when its
action points to that installation. Remove a custom `-TaskName` or `-TaskPath`
with the same helper before uninstalling.
Assisted upgrades stop an owned running default task before replacing locked
files and restart it after the new payload and registration metadata are in
place. An unrecognized task or process aborts the operation without deleting a
partial installation.

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
