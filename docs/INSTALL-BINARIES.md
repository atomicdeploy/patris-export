# Installing release binaries

GitHub Releases are built from the exact tagged source commit after the Go,
web, Linux, native Windows, and Windows cross-build checks pass. Verify the
download with the release's `SHA256SUMS` before installation.

## Windows amd64

1. Download `patris-export-vX.Y.Z-windows-amd64.zip`.
2. Extract the complete archive to a permanent directory such as
   `C:\Program Files\Patris Export`.
3. Keep `patris-export.exe`, `libpxlib.dll`, and the bundled MinGW runtime
   DLLs together. The executable is not portable without those adjacent files.
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
