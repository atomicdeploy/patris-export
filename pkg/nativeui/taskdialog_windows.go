package nativeui

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"unsafe"

	"github.com/atomicdeploy/patris-export/pkg/paradox"
	"golang.org/x/sys/windows"
)

var (
	comctl32       = windows.NewLazySystemDLL("comctl32.dll")
	procTaskDialog = comctl32.NewProc("TaskDialog")
	user32         = windows.NewLazySystemDLL("user32.dll")
	procMessageBox = user32.NewProc("MessageBoxW")
)

const (
	taskDialogCommonButtonOK = 0x0001
	messageBoxOK             = 0x00000000
	messageBoxIconError      = 0x00000010
)

// ShowNativeDependencyError displays a native Windows dialog for foreground
// failures caused by missing native runtimes. It returns true when it handled
// the error. Set PATRIS_EXPORT_NO_TASKDIALOG=1 for tests, services, or scripts
// that must never block on a modal dialog.
func ShowNativeDependencyError(err error) bool {
	if disableTaskDialog() {
		return false
	}
	var depErr *paradox.NativeDependencyError
	if !errors.As(err, &depErr) {
		return false
	}

	title := "Patris Export"
	mainInstruction := "Native Paradox reader is missing"
	content := friendlyDependencyMessage(depErr)
	if showTaskDialog(title, mainInstruction, content) == nil {
		return true
	}
	showMessageBox(title, mainInstruction+"\n\n"+content)
	return true
}

func disableTaskDialog() bool {
	value := strings.ToLower(strings.TrimSpace(os.Getenv("PATRIS_EXPORT_NO_TASKDIALOG")))
	return value == "1" || value == "true" || value == "yes" || value == "on"
}

func friendlyDependencyMessage(depErr *paradox.NativeDependencyError) string {
	var b strings.Builder
	b.WriteString("Patris Export started correctly, but the pxlib native runtime required to read .db files could not be loaded.\n\n")
	b.WriteString("Place libpxlib.dll next to patris-export.exe, or set PATRIS_EXPORT_PXLIB_ROOT / PATRIS_EXPORT_PXLIB_LIBRARY to the pxlib runtime location.")
	if depErr.Err != nil {
		fmt.Fprintf(&b, "\n\nDetails: %v", depErr.Err)
	}
	if len(depErr.Attempts) > 0 {
		b.WriteString("\n\nChecked paths:\n")
		for _, attempt := range depErr.Attempts {
			b.WriteString(" - ")
			b.WriteString(attempt)
			b.WriteByte('\n')
		}
	}
	return strings.TrimSpace(b.String())
}

func showTaskDialog(title, mainInstruction, content string) error {
	titlePtr, err := windows.UTF16PtrFromString(title)
	if err != nil {
		return err
	}
	mainPtr, err := windows.UTF16PtrFromString(mainInstruction)
	if err != nil {
		return err
	}
	contentPtr, err := windows.UTF16PtrFromString(content)
	if err != nil {
		return err
	}
	var button int32
	result, _, callErr := procTaskDialog.Call(
		0,
		0,
		uintptr(unsafe.Pointer(titlePtr)),
		uintptr(unsafe.Pointer(mainPtr)),
		uintptr(unsafe.Pointer(contentPtr)),
		uintptr(taskDialogCommonButtonOK),
		0,
		uintptr(unsafe.Pointer(&button)),
	)
	if result != 0 {
		return callErr
	}
	return nil
}

func showMessageBox(title, content string) {
	titlePtr, titleErr := windows.UTF16PtrFromString(title)
	contentPtr, contentErr := windows.UTF16PtrFromString(content)
	if titleErr != nil || contentErr != nil {
		return
	}
	procMessageBox.Call(
		0,
		uintptr(unsafe.Pointer(contentPtr)),
		uintptr(unsafe.Pointer(titlePtr)),
		uintptr(messageBoxOK|messageBoxIconError),
	)
}
