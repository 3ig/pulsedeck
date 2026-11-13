; Pulsedeck installer for Windows. Per-user: no administrator rights needed.
; Build: makensis -DVERSION=1.2.3 -DEXE=<path to pulsedeck.exe> -DOUT=<setup.exe> installer.nsi
Unicode True
!include "MUI2.nsh"

Name "Pulsedeck"
OutFile "${OUT}"
RequestExecutionLevel user
InstallDir "$LOCALAPPDATA\Programs\Pulsedeck"
InstallDirRegKey HKCU "Software\Pulsedeck" "InstallDir"
SetCompressor /SOLID lzma

!define UNINST_KEY "Software\Microsoft\Windows\CurrentVersion\Uninstall\Pulsedeck"
!define MUI_ICON "pulsedeck.ico"
!define MUI_UNICON "pulsedeck.ico"
!define MUI_FINISHPAGE_RUN "$INSTDIR\pulsedeck.exe"
!define MUI_FINISHPAGE_RUN_TEXT "Start Pulsedeck now"

!insertmacro MUI_PAGE_DIRECTORY
!insertmacro MUI_PAGE_INSTFILES
!insertmacro MUI_PAGE_FINISH
!insertmacro MUI_UNPAGE_CONFIRM
!insertmacro MUI_UNPAGE_INSTFILES
!insertmacro MUI_LANGUAGE "English"

Section "Pulsedeck"
  ; A running copy would keep the program file locked.
  nsExec::Exec 'taskkill /im pulsedeck.exe /f'
  Sleep 500
  SetOutPath "$INSTDIR"
  File "/oname=pulsedeck.exe" "${EXE}"
  File "pulsedeck.ico"
  File "/oname=LICENSE.txt" "..\..\LICENSE"
  WriteUninstaller "$INSTDIR\uninstall.exe"
  CreateShortcut "$SMPROGRAMS\Pulsedeck.lnk" "$INSTDIR\pulsedeck.exe" "" "$INSTDIR\pulsedeck.ico"

  WriteRegStr HKCU "Software\Pulsedeck" "InstallDir" "$INSTDIR"
  WriteRegStr HKCU "${UNINST_KEY}" "DisplayName" "Pulsedeck"
  WriteRegStr HKCU "${UNINST_KEY}" "DisplayVersion" "${VERSION}"
  WriteRegStr HKCU "${UNINST_KEY}" "Publisher" "Pulsedeck contributors"
  WriteRegStr HKCU "${UNINST_KEY}" "DisplayIcon" "$INSTDIR\pulsedeck.ico"
  WriteRegStr HKCU "${UNINST_KEY}" "UninstallString" '"$INSTDIR\uninstall.exe"'
  WriteRegStr HKCU "${UNINST_KEY}" "URLInfoAbout" "https://github.com/3ig/pulsedeck"
  WriteRegDWORD HKCU "${UNINST_KEY}" "NoModify" 1
  WriteRegDWORD HKCU "${UNINST_KEY}" "NoRepair" 1
SectionEnd

Section "Uninstall"
  nsExec::Exec 'taskkill /im pulsedeck.exe /f'
  Sleep 500
  Delete "$INSTDIR\pulsedeck.exe"
  Delete "$INSTDIR\pulsedeck.ico"
  Delete "$INSTDIR\LICENSE.txt"
  Delete "$INSTDIR\uninstall.exe"
  RMDir "$INSTDIR"
  Delete "$SMPROGRAMS\Pulsedeck.lnk"
  ; "Start at login", if it was switched on.
  DeleteRegValue HKCU "Software\Microsoft\Windows\CurrentVersion\Run" "Pulsedeck"
  DeleteRegKey HKCU "${UNINST_KEY}"
  DeleteRegKey HKCU "Software\Pulsedeck"
  ; Settings in %APPDATA%\pulsedeck are left in place on purpose.
SectionEnd
