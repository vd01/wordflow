//go:build windows

package main

// UI Automation (UIA) selected-text reader.
//
// On the global hotkey, we first ask the *foreground window* what text is
// currently selected — no clipboard involved, no synthetic Ctrl+C. This
// gives the user a 2-step flow in browsers/editors: select a word → press
// the hotkey.
//
// Verified empirically on Windows 10 19045 with a probe program (see
// E:/uia_probe) — do not "fix" the vtable indices from memory, the layout
// differs from older references:
//
//	IUIAutomation.GetFocusedElement         = vtable 8   (IUnknown 0..2 + own 5)
//	IUIAutomationElement.GetCurrentPattern  = vtable 14 OR 16 (2-arg, OS-dependent)
//	TextPattern.GetSelection                = vtable 5   (own 2)
//	TextRangeArray.Length / GetElement      = vtable 3 / 4
//	TextRange.GetText                       = vtable 12  (own 9)
//
// Pitfall: use the 2-arg GetCurrentPattern (patternId, IUnknown**) and NOT
// GetCurrentPatternAs. The As-variant needs IID_IUIAutomationTextPattern,
// whose GUID in the Win11 SDK (32eba289-3583-...) is not recognized by the
// Win10 system UIAutomationCore.dll → E_NOINTERFACE. GetCurrentPattern
// returns the raw pattern object whose own vtable IS the TextPattern vtable,
// so it works on both layouts. We probe slots 14 and 16 and use whichever
// write lands in the 2nd argument register (2-arg method).

import (
	"log"
	"strings"
	"syscall"
	"time"
	"unicode/utf16"
	"unsafe"
)

var (
	modOle32   = syscall.NewLazyDLL("ole32.dll")
	procCoInit = modOle32.NewProc("CoInitializeEx")
	procCoUni  = modOle32.NewProc("CoUninitialize")
	procCreate = modOle32.NewProc("CoCreateInstance")

	modOleAut   = syscall.NewLazyDLL("oleaut32.dll")
	procStrLen  = modOleAut.NewProc("SysStringLen")
	procStrFree = modOleAut.NewProc("SysFreeString")
)

type uiaGUID struct {
	Data1 uint32
	Data2 uint16
	Data3 uint16
	Data4 [8]byte
}

var (
	clsidCUIAutomation = uiaGUID{0xff48dba4, 0x60ef, 0x4201, [8]byte{0xaa, 0x87, 0x54, 0x10, 0x3e, 0xef, 0x59, 0x4e}}
	iidIUIAutomation   = uiaGUID{0x30cbe57d, 0xd9d0, 0x452a, [8]byte{0xab, 0x13, 0x7a, 0xc5, 0xac, 0x48, 0x25, 0xee}}
)

const (
	clsctxInprocServer = 0x1
	coinitApartment    = 0x2
	uiaTextPatternID   = 10014 // UIA_TextPatternId

	// uiaQueryTimeout bounds how long we wait for the foreground app to
	// answer. First call loads UIAutomationCore (~100-300ms); subsequent
	// calls are fast. A hung app falls through to the clipboard fallback.
	uiaQueryTimeout = 1200 * time.Millisecond
)

// uiaVtable returns the function pointer at vtable index idx of a COM object.
func uiaVtable(obj unsafe.Pointer, idx int) uintptr {
	vt := *(*uintptr)(obj)
	return *(*uintptr)(unsafe.Pointer(vt + uintptr(idx)*8))
}

// uiaCall invokes COM method idx with the given args.
func uiaCall(obj unsafe.Pointer, idx int, args ...uintptr) (uintptr, uintptr, error) {
	return syscall.SyscallN(uiaVtable(obj, idx), append([]uintptr{uintptr(obj)}, args...)...)
}

// uiaRelease calls IUnknown::Release (vtable idx 2).
func uiaRelease(obj unsafe.Pointer) {
	if obj != nil {
		syscall.SyscallN(uiaVtable(obj, 2), uintptr(obj))
	}
}

// bstrToGoString converts and frees a BSTR.
func bstrToGoString(bstr uintptr) string {
	if bstr == 0 {
		return ""
	}
	n, _, _ := procStrLen.Call(bstr)
	s := ""
	if n > 0 {
		s = string(utf16.Decode(unsafe.Slice((*uint16)(unsafe.Pointer(bstr)), int(n))))
	}
	procStrFree.Call(bstr)
	return s
}

// queryFocusedSelectionUIA returns the selected text of the foreground
// window's focused element, or "" if there is no selection / no text
// pattern / any error. Initializes COM on the calling thread; all COM
// objects are created and released on the same thread.
func queryFocusedSelectionUIA() string {
	hr, _, _ := procCoInit.Call(0, coinitApartment)
	// S_OK (0) or S_FALSE (1) = initialized; RPC_E_CHANGED_MODE etc = bail.
	if uint32(hr) != 0 && uint32(hr) != 1 {
		return ""
	}
	defer procCoUni.Call()

	var ua uintptr
	r, _, _ := procCreate.Call(
		uintptr(unsafe.Pointer(&clsidCUIAutomation)), 0, clsctxInprocServer,
		uintptr(unsafe.Pointer(&iidIUIAutomation)),
		uintptr(unsafe.Pointer(&ua)))
	if r != 0 || ua == 0 {
		return ""
	}
	defer uiaRelease(unsafe.Pointer(ua))

	// GetFocusedElement — the focused element of the foreground window.
	var elem uintptr
	rf, _, _ := uiaCall(unsafe.Pointer(ua), 8, uintptr(unsafe.Pointer(&elem)))
	if rf != 0 || elem == 0 {
		return ""
	}
	defer uiaRelease(unsafe.Pointer(elem))

	// GetCurrentPattern (2-arg): its vtable slot is 14 or 16 depending on
	// OS. Try both; the 2-arg variant writes into the 2nd argument register.
	var tp uintptr
	for _, idx := range []int{14, 16} {
		var a, b uintptr
		uiaCall(unsafe.Pointer(elem), idx, uiaTextPatternID, uintptr(unsafe.Pointer(&a)), uintptr(unsafe.Pointer(&b)))
		if a != 0 {
			tp = a
			break
		}
		if b != 0 {
			tp = b
			break
		}
	}
	if tp == 0 {
		return ""
	}
	defer uiaRelease(unsafe.Pointer(tp))

	// GetSelection → TextRangeArray.
	var arr uintptr
	rs, _, _ := uiaCall(unsafe.Pointer(tp), 5, uintptr(unsafe.Pointer(&arr)))
	if rs != 0 || arr == 0 {
		return ""
	}
	defer uiaRelease(unsafe.Pointer(arr))

	var n int
	uiaCall(unsafe.Pointer(arr), 3, uintptr(unsafe.Pointer(&n))) // Length
	if n <= 0 {
		return ""
	}

	parts := make([]string, 0, n)
	for i := 0; i < n; i++ {
		var rng uintptr
		uiaCall(unsafe.Pointer(arr), 4, uintptr(i), uintptr(unsafe.Pointer(&rng))) // GetElement
		if rng == 0 {
			continue
		}
		var bstr uintptr
		// GetText(maxLength=-1, &bstr)
		uiaCall(unsafe.Pointer(rng), 12, uintptr(^uint32(0)), uintptr(unsafe.Pointer(&bstr)))
		if bstr != 0 {
			parts = append(parts, bstrToGoString(bstr))
		}
		uiaRelease(unsafe.Pointer(rng))
	}
	return strings.Join(parts, " ")
}

// getFocusedSelectionTextUIA runs the UIA query off the main thread with a
// timeout so a slow or hung foreground app cannot freeze the WordFlow UI.
// Returns "" on timeout/failure — the caller falls back to the clipboard.
func getFocusedSelectionTextUIA() string {
	ch := make(chan string, 1)
	go func() {
		ch <- queryFocusedSelectionUIA()
	}()
	select {
	case s := <-ch:
		return s
	case <-time.After(uiaQueryTimeout):
		log.Printf("[UIA] selection query timed out after %v", uiaQueryTimeout)
		return ""
	}
}
