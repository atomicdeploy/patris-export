; Extend the NSIS-provided Farsi MUI2 translation with the MultiUser page
; strings that are missing from NSIS 3.0.x through 3.12.x.
!include "${NSISDIR}\Contrib\Language files\Farsi.nsh"

!ifdef MULTIUSER_INSTALLMODEPAGE
  ${LangFileString} MULTIUSER_TEXT_INSTALLMODE_TITLE "نوع نصب"
  ${LangFileString} MULTIUSER_TEXT_INSTALLMODE_SUBTITLE "مشخص کنید برنامه برای کدام کاربران نصب شود."
  ${LangFileString} MULTIUSER_INNERTEXT_INSTALLMODE_TOP "Patris Export را می‌توانید فقط برای حساب خود یا برای همه کاربران این رایانه نصب کنید. $(^ClickNext)"
  ${LangFileString} MULTIUSER_INNERTEXT_INSTALLMODE_ALLUSERS "نصب برای همه کاربران (نیازمند دسترسی مدیر)"
  ${LangFileString} MULTIUSER_INNERTEXT_INSTALLMODE_CURRENTUSER "نصب فقط برای من"
!endif
