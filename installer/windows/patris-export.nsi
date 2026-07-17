Unicode true
SetCompressor /SOLID lzma
SetCompressorDictSize 64

!ifndef PRODUCT_VERSION
  !error "PRODUCT_VERSION must be provided by Build-Installer.ps1"
!endif
!ifndef PRODUCT_VERSION_QUAD
  !error "PRODUCT_VERSION_QUAD must be provided by Build-Installer.ps1"
!endif
!ifndef INSTALLER_FILENAME
  !error "INSTALLER_FILENAME must be provided by Build-Installer.ps1"
!endif
!ifndef PAYLOAD_DIR
  !error "PAYLOAD_DIR must be provided by Build-Installer.ps1"
!endif
!ifndef OUTPUT_DIR
  !error "OUTPUT_DIR must be provided by Build-Installer.ps1"
!endif
!ifndef ASSET_DIR
  !error "ASSET_DIR must be provided by Build-Installer.ps1"
!endif

!define PRODUCT_NAME "Patris Export"
!define PRODUCT_PUBLISHER "Atomic Deploy"
!define PRODUCT_WEB_SITE "https://github.com/atomicdeploy/patris-export"
!define PRODUCT_REG_KEY "Software\AtomicDeploy\Patris Export"
!define PRODUCT_UNINSTALL_KEY "Software\Microsoft\Windows\CurrentVersion\Uninstall\Patris Export"

!ifdef CURRENT_USER_ONLY
  !define MULTIUSER_EXECUTIONLEVEL User
!else
  !define MULTIUSER_EXECUTIONLEVEL Highest
!endif
!define MULTIUSER_INSTALLMODE_COMMANDLINE
!define MULTIUSER_INSTALLMODE_DEFAULT_CURRENTUSER
!define MULTIUSER_INSTALLMODE_INSTDIR "Patris Export"
!define MULTIUSER_INSTALLMODE_INSTDIR_REGISTRY_KEY "${PRODUCT_REG_KEY}"
!define MULTIUSER_INSTALLMODE_INSTDIR_REGISTRY_VALUENAME "InstallLocation"
!ifndef CURRENT_USER_ONLY
  !define MULTIUSER_INSTALLMODE_DEFAULT_REGISTRY_KEY "${PRODUCT_REG_KEY}"
  !define MULTIUSER_INSTALLMODE_DEFAULT_REGISTRY_VALUENAME "InstallLocation"
!endif
!define MULTIUSER_INSTALLMODE_FUNCTION PatrisSetInstallDirectory
!define MULTIUSER_USE_PROGRAMFILES64
!define MULTIUSER_MUI

!include "MultiUser.nsh"
!include "FileFunc.nsh"
!include "LogicLib.nsh"
!include "Sections.nsh"
!include "nsDialogs.nsh"

Name "${PRODUCT_NAME} ${PRODUCT_VERSION}"
OutFile "${OUTPUT_DIR}\${INSTALLER_FILENAME}"
InstallDir "$LOCALAPPDATA\Programs\Patris Export"
BrandingText "${PRODUCT_NAME} · ${PRODUCT_PUBLISHER}"
CRCCheck on
ShowInstDetails show
ShowUninstDetails show

VIProductVersion "${PRODUCT_VERSION_QUAD}"
VIAddVersionKey /LANG=1033 "ProductName" "${PRODUCT_NAME}"
VIAddVersionKey /LANG=1033 "ProductVersion" "${PRODUCT_VERSION}"
VIAddVersionKey /LANG=1033 "FileDescription" "${PRODUCT_NAME} assisted installer"
VIAddVersionKey /LANG=1033 "FileVersion" "${PRODUCT_VERSION}"
VIAddVersionKey /LANG=1033 "CompanyName" "${PRODUCT_PUBLISHER}"
VIAddVersionKey /LANG=1033 "LegalCopyright" "Copyright © 2025-2026 ${PRODUCT_PUBLISHER}"
VIAddVersionKey /LANG=1033 "Comments" "Source commit ${SOURCE_COMMIT}"

Icon "${ICON_FILE}"
UninstallIcon "${ICON_FILE}"

!define MUI_ABORTWARNING
!define MUI_ICON "${ICON_FILE}"
!define MUI_UNICON "${ICON_FILE}"
!define MUI_HEADERIMAGE
!define MUI_HEADERIMAGE_RIGHT
!define MUI_HEADERIMAGE_BITMAP "${ASSET_DIR}\installer-header.bmp"
!define MUI_HEADERIMAGE_UNBITMAP "${ASSET_DIR}\installer-header.bmp"
!define MUI_WELCOMEFINISHPAGE_BITMAP "${ASSET_DIR}\installer-sidebar.bmp"
!define MUI_UNWELCOMEFINISHPAGE_BITMAP "${ASSET_DIR}\uninstaller-sidebar.bmp"
!define MUI_FINISHPAGE_SHOWREADME "$INSTDIR\INSTALLER.md"
!define MUI_FINISHPAGE_SHOWREADME_TEXT "Open the installation and configuration guide"
!define MUI_FINISHPAGE_SHOWREADME_NOTCHECKED
!define MUI_FINISHPAGE_LINK "Project source, support, and release history"
!define MUI_FINISHPAGE_LINK_LOCATION "${PRODUCT_WEB_SITE}"

!insertmacro MUI_PAGE_WELCOME
!insertmacro MUI_PAGE_LICENSE "${LICENSE_FILE}"
!ifndef CURRENT_USER_ONLY
  !insertmacro MULTIUSER_PAGE_INSTALLMODE
!endif
!insertmacro MUI_PAGE_COMPONENTS
!insertmacro MUI_PAGE_DIRECTORY
!insertmacro MUI_PAGE_INSTFILES
!insertmacro MUI_PAGE_FINISH

!insertmacro MUI_UNPAGE_WELCOME
!insertmacro MUI_UNPAGE_CONFIRM
UninstPage custom un.CleanupPageCreate un.CleanupPageLeave
!insertmacro MUI_UNPAGE_INSTFILES
!insertmacro MUI_UNPAGE_FINISH

!insertmacro MUI_LANGUAGE "English"
!insertmacro MUI_LANGUAGEEX "${LANGUAGE_DIR}" "Farsi"
!insertmacro MUI_RESERVEFILE_LANGDLL

LangString CleanupTitle 1033 "Configuration and data"
LangString CleanupTitle 1065 "پیکربندی و داده‌ها"
LangString CleanupSubtitle 1033 "Choose whether locally stored settings should be retained."
LangString CleanupSubtitle 1065 "مشخص کنید تنظیمات ذخیره‌شده نگهداری شوند یا خیر."
LangString CleanupIntro 1033 "Patris Export keeps configuration outside the installation folder so upgrades and normal uninstallations do not erase your settings."
LangString CleanupIntro 1065 "Patris Export پیکربندی را بیرون از پوشه نصب نگه می‌دارد تا ارتقا و حذف معمولی تنظیمات شما را پاک نکند."
LangString CleanupOption 1033 "Also remove Patris Export configuration, cached data, and local license state"
LangString CleanupOption 1065 "پیکربندی، داده‌های موقت و وضعیت مجوز محلی Patris Export نیز حذف شود"
LangString CleanupWarning 1033 "Leave this unchecked unless you are permanently removing the application. This action cannot be undone."
LangString CleanupWarning 1065 "این گزینه را فقط هنگام حذف دائمی برنامه انتخاب کنید. این کار قابل بازگشت نیست."

Var CleanupCheckbox
Var RemoveUserData
Var InitialInstallDirectory

Function PatrisSetInstallDirectory
  ReadRegStr $0 SHCTX "${PRODUCT_REG_KEY}" "InstallLocation"
  ${If} $0 == ""
    ${If} $MultiUser.InstallMode == "CurrentUser"
      StrCpy $INSTDIR "$LOCALAPPDATA\Programs\Patris Export"
    ${EndIf}
  ${EndIf}
FunctionEnd

Function .onInit
  ; NSIS applies the special /D= destination before .onInit. Preserve that
  ; value across MultiUser initialization, which otherwise selects its default
  ; or the previous registered install location.
  StrCpy $InitialInstallDirectory $INSTDIR
  !insertmacro MULTIUSER_INIT
  ${If} $InitialInstallDirectory != "$LOCALAPPDATA\Programs\Patris Export"
    StrCpy $INSTDIR $InitialInstallDirectory
  ${EndIf}
  !insertmacro MUI_LANGDLL_DISPLAY
FunctionEnd

Function un.onInit
  !insertmacro MULTIUSER_UNINIT
  !insertmacro MUI_UNGETLANGUAGE
  StrCpy $RemoveUserData "0"
  ${GetParameters} $0
  ClearErrors
  ${GetOptions} $0 "/PURGEDATA" $1
  ${IfNot} ${Errors}
    StrCpy $RemoveUserData "1"
  ${EndIf}
FunctionEnd

Function un.CleanupPageCreate
  !insertmacro MUI_HEADER_TEXT "$(CleanupTitle)" "$(CleanupSubtitle)"
  nsDialogs::Create 1018
  Pop $0
  ${If} $0 == error
    Abort
  ${EndIf}

  ${NSD_CreateLabel} 0 0 100% 38u "$(CleanupIntro)"
  Pop $1
  ${NSD_CreateCheckbox} 0 48u 100% 24u "$(CleanupOption)"
  Pop $CleanupCheckbox
  ${If} $RemoveUserData == "1"
    ${NSD_Check} $CleanupCheckbox
  ${EndIf}
  ${NSD_CreateLabel} 0 82u 100% 36u "$(CleanupWarning)"
  Pop $1
  nsDialogs::Show
FunctionEnd

Function un.CleanupPageLeave
  ${NSD_GetState} $CleanupCheckbox $0
  ${If} $0 == ${BST_CHECKED}
    StrCpy $RemoveUserData "1"
  ${Else}
    StrCpy $RemoveUserData "0"
  ${EndIf}
FunctionEnd

Section "!Patris Export application" SEC_CORE
  SectionIn RO
  SetOutPath "$INSTDIR"
  SetOverwrite on

  ; Remove optional files from an older installation. Selected sections below
  ; recreate them, while deselecting a component removes its previous copy.
  Delete "$INSTDIR\patris-export.dll"
  Delete "$INSTDIR\patris-export.h"
  Delete "$DESKTOP\Patris Export.lnk"

  File /oname=patris-export.exe "${PAYLOAD_DIR}\patris-export.exe"
  File /oname=libpxlib.dll "${PAYLOAD_DIR}\libpxlib.dll"
  File /oname=libgcc_s_seh-1.dll "${PAYLOAD_DIR}\libgcc_s_seh-1.dll"
  File /oname=libstdc++-6.dll "${PAYLOAD_DIR}\libstdc++-6.dll"
  File /oname=libwinpthread-1.dll "${PAYLOAD_DIR}\libwinpthread-1.dll"
  File /oname=README.md "${PAYLOAD_DIR}\README.md"
  File /oname=INSTALL.md "${PAYLOAD_DIR}\INSTALL.md"
  File /oname=BUILD-MANIFEST.txt "${PAYLOAD_DIR}\BUILD-MANIFEST.txt"
  !if /FileExists "${PAYLOAD_DIR}\BUILD-VARIANT.json"
    File /oname=BUILD-VARIANT.json "${PAYLOAD_DIR}\BUILD-VARIANT.json"
  !endif
  File /oname=LICENSE.txt "${LICENSE_FILE}"
  File /oname=NOTICE.txt "${NOTICE_FILE}"
  File /oname=CHANGELOG.md "${CHANGELOG_FILE}"
  File /oname=LICENSING.md "${LICENSING_GUIDE}"
  File /oname=INSTALLER.md "${INSTALLER_GUIDE}"
  File /oname=config.example.toml "${CONFIG_EXAMPLE}"

  WriteUninstaller "$INSTDIR\Uninstall.exe"

  ${If} $MultiUser.InstallMode == "AllUsers"
    StrCpy $9 "/AllUsers"
  ${Else}
    StrCpy $9 "/CurrentUser"
  ${EndIf}

  CreateDirectory "$SMPROGRAMS\Patris Export"
  CreateShortCut "$SMPROGRAMS\Patris Export\Patris Export.lnk" "$INSTDIR\patris-export.exe" "view" "$INSTDIR\patris-export.exe" 0 SW_SHOWNORMAL "" "Open the configured Patris database viewer"
  CreateShortCut "$SMPROGRAMS\Patris Export\Configuration Guide.lnk" "$INSTDIR\INSTALLER.md"
  CreateShortCut "$SMPROGRAMS\Patris Export\Licensing Guide.lnk" "$INSTDIR\LICENSING.md"
  CreateShortCut "$SMPROGRAMS\Patris Export\Uninstall Patris Export.lnk" "$INSTDIR\Uninstall.exe" "$9"

  SetRegView 64
  WriteRegStr SHCTX "${PRODUCT_REG_KEY}" "InstallLocation" "$INSTDIR"
  WriteRegStr SHCTX "${PRODUCT_REG_KEY}" "Version" "${PRODUCT_VERSION}"
  WriteRegStr SHCTX "Software\Microsoft\Windows\CurrentVersion\App Paths\patris-export.exe" "" "$INSTDIR\patris-export.exe"
  WriteRegStr SHCTX "Software\Microsoft\Windows\CurrentVersion\App Paths\patris-export.exe" "Path" "$INSTDIR"

  WriteRegStr SHCTX "${PRODUCT_UNINSTALL_KEY}" "DisplayName" "${PRODUCT_NAME}"
  WriteRegStr SHCTX "${PRODUCT_UNINSTALL_KEY}" "DisplayVersion" "${PRODUCT_VERSION}"
  WriteRegStr SHCTX "${PRODUCT_UNINSTALL_KEY}" "DisplayIcon" "$INSTDIR\patris-export.exe"
  WriteRegStr SHCTX "${PRODUCT_UNINSTALL_KEY}" "Publisher" "${PRODUCT_PUBLISHER}"
  WriteRegStr SHCTX "${PRODUCT_UNINSTALL_KEY}" "URLInfoAbout" "${PRODUCT_WEB_SITE}"
  WriteRegStr SHCTX "${PRODUCT_UNINSTALL_KEY}" "InstallLocation" "$INSTDIR"
  WriteRegStr SHCTX "${PRODUCT_UNINSTALL_KEY}" "UninstallString" "$\"$INSTDIR\Uninstall.exe$\" $9"
  WriteRegStr SHCTX "${PRODUCT_UNINSTALL_KEY}" "QuietUninstallString" "$\"$INSTDIR\Uninstall.exe$\" /S $9"
  WriteRegDWORD SHCTX "${PRODUCT_UNINSTALL_KEY}" "NoModify" 1
  WriteRegDWORD SHCTX "${PRODUCT_UNINSTALL_KEY}" "NoRepair" 1
  ${GetSize} "$INSTDIR" "/S=0K" $0 $1 $2
  WriteRegDWORD SHCTX "${PRODUCT_UNINSTALL_KEY}" "EstimatedSize" $0
SectionEnd

Section "Developer integration SDK (DLL and C header)" SEC_SDK
  SetOutPath "$INSTDIR"
  File /oname=patris-export.dll "${PAYLOAD_DIR}\patris-export.dll"
  File /oname=patris-export.h "${PAYLOAD_DIR}\patris-export.h"
SectionEnd

Section "Desktop shortcut" SEC_DESKTOP
  CreateShortCut "$DESKTOP\Patris Export.lnk" "$INSTDIR\patris-export.exe" "view" "$INSTDIR\patris-export.exe" 0 SW_SHOWNORMAL "" "Open the configured Patris database viewer"
SectionEnd

!insertmacro MUI_FUNCTION_DESCRIPTION_BEGIN
  !insertmacro MUI_DESCRIPTION_TEXT ${SEC_CORE} "The Patris Export command-line utility, native runtime, Web UI resources, license, changelog, and documentation."
  !insertmacro MUI_DESCRIPTION_TEXT ${SEC_SDK} "C-compatible shared library and header for embedding Patris Export in another application."
  !insertmacro MUI_DESCRIPTION_TEXT ${SEC_DESKTOP} "Create a convenient shortcut on the current desktop."
!insertmacro MUI_FUNCTION_DESCRIPTION_END

Section "Uninstall"
  SetRegView 64
  Delete "$DESKTOP\Patris Export.lnk"
  Delete "$SMPROGRAMS\Patris Export\Patris Export.lnk"
  Delete "$SMPROGRAMS\Patris Export\Configuration Guide.lnk"
  Delete "$SMPROGRAMS\Patris Export\Licensing Guide.lnk"
  Delete "$SMPROGRAMS\Patris Export\Uninstall Patris Export.lnk"
  RMDir "$SMPROGRAMS\Patris Export"

  DeleteRegKey SHCTX "Software\Microsoft\Windows\CurrentVersion\App Paths\patris-export.exe"
  DeleteRegKey SHCTX "${PRODUCT_UNINSTALL_KEY}"
  DeleteRegKey SHCTX "${PRODUCT_REG_KEY}"
  DeleteRegKey /ifempty SHCTX "Software\AtomicDeploy"

  Delete "$INSTDIR\patris-export.exe"
  Delete "$INSTDIR\patris-export.dll"
  Delete "$INSTDIR\patris-export.h"
  Delete "$INSTDIR\libpxlib.dll"
  Delete "$INSTDIR\libgcc_s_seh-1.dll"
  Delete "$INSTDIR\libstdc++-6.dll"
  Delete "$INSTDIR\libwinpthread-1.dll"
  Delete "$INSTDIR\README.md"
  Delete "$INSTDIR\INSTALL.md"
  Delete "$INSTDIR\BUILD-MANIFEST.txt"
  Delete "$INSTDIR\BUILD-VARIANT.json"
  Delete "$INSTDIR\LICENSE.txt"
  Delete "$INSTDIR\NOTICE.txt"
  Delete "$INSTDIR\CHANGELOG.md"
  Delete "$INSTDIR\LICENSING.md"
  Delete "$INSTDIR\INSTALLER.md"
  Delete "$INSTDIR\config.example.toml"
  Delete "$INSTDIR\Uninstall.exe"
  RMDir "$INSTDIR"

  ${If} $RemoveUserData == "1"
    !ifdef PURGE_DATA_ROOT
      ; Test-only current-user builds use an isolated cleanup root so the
      ; complete purge flow can be exercised without touching live settings.
      RMDir /r "${PURGE_DATA_ROOT}"
    !else
      RMDir /r "$APPDATA\Patris Export"
      RMDir /r "$LOCALAPPDATA\Patris Export"
    !endif
  ${EndIf}
SectionEnd
