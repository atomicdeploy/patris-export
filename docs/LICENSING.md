# Optional ALM Compatibility Licensing

Patris Export is license-free by default. Licensing is compiled in only when a
Windows build explicitly sets `ENABLE_ALM=1` and supplies `ALM_APP_ID`. The
standard executable and shared library contain the same license-management ABI
symbols, but report `mode: "none"`, never block engine creation, and do not
require a key.

## Build Profiles

Standard build:

```powershell
.\scripts\windows\Build-LocalWindows.ps1
```

Optional licensed build:

```powershell
$env:ENABLE_ALM = "1"
$env:ALM_APP_ID = "Digitalogic-Patris-ALM-v1"
.\scripts\windows\Build-LocalWindows.ps1
```

The Bash helper and Makefile accept the same environment variables:

```bash
ENABLE_ALM=1 ALM_APP_ID=Digitalogic-Patris-ALM-v1 \
  ./build.sh --target windows

ENABLE_ALM=1 ALM_APP_ID=Digitalogic-Patris-ALM-v1 \
  make build-windows build-lib-windows
```

`ALM_APP_ID` is required and should be a stable identifier containing 6-128
letters, digits, `.`, `_`, `:`, `@`, `+`, or `-`. It is linked into the tagged
binary as a **public derivation input**, not a secret or private key. Build
artifacts never write it to `BUILD-VARIANT.json`.

The local Windows helper keeps variants separate:

- Standard staging: `AtomicDeploy/build/patris-export-windows-amd64`
- ALM staging: `AtomicDeploy/build/patris-export-windows-amd64-alm-compat`
- Standard live deployment: `AtomicDeploy/deploy`
- ALM live deployment: `AtomicDeploy/deploy/alm-compat`

Internal executable and DLL names remain stable so host loaders do not need
variant-specific code. ALM release artifacts use the `-alm-compat` suffix. Each
build includes `BUILD-VARIANT.json` with `variant`, `licensing_mode`, and
`license_required`; it never contains the application identifier or a key.

The ALM compatibility profile is Windows-only because its challenge uses
Windows WMI/CIM. Build helpers refuse to create a Linux ALM runtime. Standard
Patris Export remains fully supported as a standalone cross-platform build.

## Compatibility Profile

The profile name is `alm_compat_utf8_v1`. It intentionally fixes the
algorithm so builds and language runtimes cannot silently disagree:

1. Query `Win32_BaseBoard.SerialNumber` and
   `Win32_Processor.ProcessorId` through Windows CIM/WMI.
2. Concatenate the two values without a separator.
3. Compute SHA-256 over the exact string encoded as UTF-8, without a trailing
   NUL, and render exactly 64 uppercase hexadecimal characters. This
   is the machine challenge.
4. Concatenate the challenge and embedded application identifier.
5. Apply the same UTF-8 SHA-256 operation to produce the accepted key.

Golden interoperability vector:

```text
BaseBoard SerialNumber: BOARD-1234
ProcessorId: BFEBFBFF000906EA
Application identifier: Digitalogic-Patris-ALM-v1
Challenge: CED6E29936807D6E58D036519A5DBB96348593576466264017E841B977E85DB4
Key: 7933A7D613DD83057C736E645C0116509F7B0DEDB433976A0CF618BCCA7C5DC7
```

Although a stale comment in AHK_ALM's
[AutoHotkey v2 hash helper](https://github.com/Rayan-Refoua/AHK_ALM/blob/main/libs/BcryptHash_ahk2.ahk)
mentions UTF-16, its active string branch calls `StrPut(item, "UTF-8")`. Patris
Export follows that executable behavior exactly. The explicit versioned profile
and golden test prevent a future comment or implementation change from silently
altering the compatibility contract.

## Key Discovery And Management

The authoritative writable key is per-user:

```text
%APPDATA%\Patris Export\license.key
```

`PATRIS_EXPORT_LICENSE_FILE` can override this path for managed deployments and
tests. If the per-user file is absent, Patris Export also discovers a legacy
file named `key` beside the executable. The legacy file is read-only: install
and remove operations never create, replace, or delete it. An existing invalid
per-user key is authoritative and is not bypassed by a legacy key.

CLI management commands bypass engine enforcement so an unlicensed machine can
be activated:

```powershell
patris-export license status
patris-export license challenge
patris-export license install 64_CHARACTER_KEY
patris-export license install --file C:\Path\license.key
patris-export license remove
```

`status`, successful `install`, and `remove` print JSON suitable for an
installer or Electron host. `remove` deletes only the per-user key.

The stable C ABI provides the same management operations:

- `PatrisExportLicenseStatusJSON()`
- `PatrisExportLicenseChallenge()`
- `PatrisExportLicenseInstall(char* key)`
- `PatrisExportLicenseRemove()`

Returned strings use the normal ABI ownership rule and must be released with
`PatrisExportFreeString`. An ALM build rejects `PatrisExportCreate` and normal
CLI operations until the status is licensed. Read the error immediately using
`PatrisExportLastError`.

## Security Scope And Limitations

This compatibility mechanism is an offline machine-binding deterrent, not a
cryptographic software-entitlement system. It embeds no secret/private key and
does not provide signatures, expiration, seats, revocation, server validation,
anti-tamper protection, or resistance to a user who can patch the executable.
Anyone who knows the public application identifier and algorithm can derive a
key. For stronger licensing, keep the public status/challenge/install ABI but
replace validation with signed license documents or an authenticated licensing
service; never embed a signing private key in an application.

The implementation derives from the MIT-licensed
[AHK_ALM project](https://github.com/Rayan-Refoua/AHK_ALM). Attribution and its
MIT terms are included in the repository `NOTICE` file. The original
[AutoHotkey community discussion](https://www.autohotkey.com/boards/viewtopic.php?style=19&t=122086)
provides additional upstream context.
