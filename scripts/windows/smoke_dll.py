"""Load and exercise the published Patris Export C ABI on Windows."""

from __future__ import annotations

import ctypes
import json
import os
import pathlib
import sys


def main() -> int:
    if len(sys.argv) != 3:
        raise SystemExit("usage: smoke_dll.py <patris-export.dll> <database.db>")

    dll_path = pathlib.Path(sys.argv[1]).resolve(strict=True)
    database = pathlib.Path(sys.argv[2]).resolve(strict=True)
    os.environ.setdefault("PATRIS_EXPORT_PXLIB_ROOT", str(dll_path.parent))
    os.environ["PATH"] = str(dll_path.parent) + os.pathsep + os.environ.get("PATH", "")
    dll_directory = os.add_dll_directory(str(dll_path.parent))
    try:
        library = ctypes.WinDLL(str(dll_path))
        configure(library)
        abi = library.PatrisExportABIVersion()
        if abi != 1:
            raise RuntimeError(f"unexpected Patris Export ABI version: {abi}")

        capabilities = take_json(library, library.PatrisExportCapabilitiesJSON())
        if capabilities.get("abi_version") != 1 or "direct" not in capabilities.get("transports", []):
            raise RuntimeError(f"invalid capabilities document: {capabilities!r}")

        options = json.dumps({
            "database_path": str(database),
            "watch": False,
            "watch_set": True,
        }).encode("utf-8")
        handle = library.PatrisExportCreate(options)
        if not handle:
            raise RuntimeError(last_error(library, "PatrisExportCreate failed"))
        try:
            info = call(library, handle, 1, "info.get")
            if info.get("num_records", 0) < 1:
                raise RuntimeError(f"DLL info smoke returned no records: {info!r}")
            records = call(library, handle, 2, "records.list")
            if not isinstance(records, dict) or len(records) < 1:
                raise RuntimeError("DLL records smoke returned an empty or invalid result")
            print(json.dumps({
                "abi": abi,
                "version": capabilities.get("product", {}).get("version"),
                "records": len(records),
            }))
        finally:
            if not library.PatrisExportClose(handle):
                raise RuntimeError(last_error(library, "PatrisExportClose failed"))
    finally:
        dll_directory.close()
    return 0


def configure(library: ctypes.WinDLL) -> None:
    library.PatrisExportABIVersion.argtypes = []
    library.PatrisExportABIVersion.restype = ctypes.c_uint32
    for name in ("PatrisExportCapabilitiesJSON", "PatrisExportLastError"):
        function = getattr(library, name)
        function.argtypes = []
        function.restype = ctypes.c_void_p
    library.PatrisExportCreate.argtypes = [ctypes.c_char_p]
    library.PatrisExportCreate.restype = ctypes.c_uint64
    library.PatrisExportCall.argtypes = [ctypes.c_uint64, ctypes.c_char_p]
    library.PatrisExportCall.restype = ctypes.c_void_p
    library.PatrisExportClose.argtypes = [ctypes.c_uint64]
    library.PatrisExportClose.restype = ctypes.c_int
    library.PatrisExportFreeString.argtypes = [ctypes.c_void_p]
    library.PatrisExportFreeString.restype = None


def take_json(library: ctypes.WinDLL, pointer: int | None) -> dict:
    if not pointer:
        raise RuntimeError(last_error(library, "Patris Export returned a null string"))
    try:
        return json.loads(ctypes.string_at(pointer).decode("utf-8"))
    finally:
        library.PatrisExportFreeString(pointer)


def last_error(library: ctypes.WinDLL, fallback: str) -> str:
    pointer = library.PatrisExportLastError()
    if not pointer:
        return fallback
    try:
        return ctypes.string_at(pointer).decode("utf-8") or fallback
    finally:
        library.PatrisExportFreeString(pointer)


def call(library: ctypes.WinDLL, handle: int, request_id: int, method: str) -> dict:
    request = json.dumps({"id": request_id, "method": method}).encode("utf-8")
    response = take_json(library, library.PatrisExportCall(handle, request))
    if not response.get("ok"):
        raise RuntimeError(response.get("error") or f"{method} failed")
    return response.get("result")


if __name__ == "__main__":
    raise SystemExit(main())
