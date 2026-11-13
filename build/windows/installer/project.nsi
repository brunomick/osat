Unicode true

####
## Instalador de osat para Windows. Parte de la plantilla de Wails 2.16 (wails build -nsis lo usa en vez
## de generar el suyo; wails_tools.nsh y tmp\ los genera cada build).
##
## osat se instala para el usuario, en %LOCALAPPDATA%\Programs\OSAT, sin pedir permisos de administrador:
## así se puede actualizar solo (internal/updater) sin que IT tenga que dar permisos cada vez.
##
## Las versiones hasta la 1.1.0 se instalaban para todos los usuarios en Archivos de programa, y su
## actualizador corre este instalador con permisos de administrador. En ese caso (instalación vieja y
## el instalador con permisos) se actualiza esa copia en su lugar, como antes: si se instalara para el
## usuario, quedaría en la carpeta de quien dio los permisos, que en una PC de empresa es IT. Desde la
## versión nueva, osat actualiza sin pedir permisos y pasa a la instalación por usuario (ver
## internal/updater/apply_windows.go).
####
!define REQUEST_EXECUTION_LEVEL "user"
!define WAILS_INSTALL_SCOPE "user"

!include "wails_tools.nsh"

# The version information for this two must consist of 4 parts
VIProductVersion "${INFO_PRODUCTVERSION}.0"
VIFileVersion    "${INFO_PRODUCTVERSION}.0"

VIAddVersionKey "CompanyName"     "${INFO_COMPANYNAME}"
VIAddVersionKey "FileDescription" "${INFO_PRODUCTNAME} Installer"
VIAddVersionKey "ProductVersion"  "${INFO_PRODUCTVERSION}"
VIAddVersionKey "FileVersion"     "${INFO_PRODUCTVERSION}"
VIAddVersionKey "LegalCopyright"  "${INFO_COPYRIGHT}"
VIAddVersionKey "ProductName"     "${INFO_PRODUCTNAME}"

# Enable HiDPI support. https://nsis.sourceforge.io/Reference/ManifestDPIAware
ManifestDPIAware true

!include "MUI.nsh"

!define MUI_ICON "..\icon.ico"
!define MUI_UNICON "..\icon.ico"
!define MUI_FINISHPAGE_NOAUTOCLOSE
!define MUI_ABORTWARNING

!insertmacro MUI_PAGE_WELCOME
!insertmacro MUI_PAGE_DIRECTORY
!insertmacro MUI_PAGE_INSTFILES
!insertmacro MUI_PAGE_FINISH

!insertmacro MUI_UNPAGE_INSTFILES

!insertmacro MUI_LANGUAGE "English"

Name "${INFO_PRODUCTNAME}"
OutFile "..\..\bin\${INFO_PROJECTNAME}-${ARCH}-installer.exe"
InstallDir "$LOCALAPPDATA\Programs\${INFO_PRODUCTNAME}"
# Una actualización va a la misma carpeta que la instalación anterior, aunque se haya elegido otra.
InstallDirRegKey HKCU "${UNINST_KEY}" "InstallLocation"
ShowInstDetails show

# 1 si se actualiza una instalación vieja para todos los usuarios (en Archivos de programa).
Var AllUsers

Function .onInit
    !insertmacro wails.checkArchitecture

    StrCpy $AllUsers 0
    SetRegView 64
    ReadRegStr $0 HKLM "${UNINST_KEY}" "DisplayIcon"
    ${If} $0 != ""
        UserInfo::GetAccountType
        Pop $1
        ${If} $1 == "Admin"
            StrCpy $AllUsers 1
            ${GetParent} $0 $INSTDIR
        ${EndIf}
    ${EndIf}
FunctionEnd

Section
    ${If} $AllUsers == 1
        SetShellVarContext all
    ${Else}
        SetShellVarContext current
    ${EndIf}

    !insertmacro wails.webview2runtime

    SetOutPath $INSTDIR

    !insertmacro wails.files

    CreateShortcut "$SMPROGRAMS\${INFO_PRODUCTNAME}.lnk" "$INSTDIR\${PRODUCT_EXECUTABLE}"
    CreateShortCut "$DESKTOP\${INFO_PRODUCTNAME}.lnk" "$INSTDIR\${PRODUCT_EXECUTABLE}"

    WriteUninstaller "$INSTDIR\uninstall.exe"

    # SHCTX es HKLM o HKCU según SetShellVarContext.
    SetRegView 64
    WriteRegStr SHCTX "${UNINST_KEY}" "Publisher" "${INFO_COMPANYNAME}"
    WriteRegStr SHCTX "${UNINST_KEY}" "DisplayName" "${INFO_PRODUCTNAME}"
    WriteRegStr SHCTX "${UNINST_KEY}" "DisplayVersion" "${INFO_PRODUCTVERSION}"
    WriteRegStr SHCTX "${UNINST_KEY}" "DisplayIcon" "$INSTDIR\${PRODUCT_EXECUTABLE}"
    WriteRegStr SHCTX "${UNINST_KEY}" "InstallLocation" "$INSTDIR"
    WriteRegStr SHCTX "${UNINST_KEY}" "UninstallString" "$\"$INSTDIR\uninstall.exe$\""
    WriteRegStr SHCTX "${UNINST_KEY}" "QuietUninstallString" "$\"$INSTDIR\uninstall.exe$\" /S"
    ${GetSize} "$INSTDIR" "/S=0K" $0 $1 $2
    IntFmt $0 "0x%08X" $0
    WriteRegDWORD SHCTX "${UNINST_KEY}" "EstimatedSize" "$0"
SectionEnd

Section "uninstall"
    # Es la instalación para todos los usuarios si la de HKLM apunta a esta carpeta.
    SetShellVarContext current
    SetRegView 64
    ReadRegStr $0 HKLM "${UNINST_KEY}" "DisplayIcon"
    ${If} $0 != ""
        ${GetParent} $0 $1
        ${If} $1 == $INSTDIR
            SetShellVarContext all
        ${EndIf}
    ${EndIf}

    # Solo lo que puso el instalador: la carpeta puede ser una elegida a mano con otras cosas adentro.
    Delete "$INSTDIR\${PRODUCT_EXECUTABLE}"
    Delete "$INSTDIR\uninstall.exe"
    RMDir $INSTDIR

    Delete "$SMPROGRAMS\${INFO_PRODUCTNAME}.lnk"
    Delete "$DESKTOP\${INFO_PRODUCTNAME}.lnk"

    DeleteRegKey SHCTX "${UNINST_KEY}"
SectionEnd
