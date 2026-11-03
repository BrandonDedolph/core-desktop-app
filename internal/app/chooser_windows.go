//go:build windows

package app

import (
	"os"
	"syscall"

	"golang.org/x/sys/windows"
)

// listDrives is every drive letter whose root answers, with its volume
// label. It runs on the chooser's reading goroutine: a network drive or an
// empty card reader can take a moment to say no.
func listDrives() []driveInfo {
	mask, err := windows.GetLogicalDrives()
	if err != nil {
		return nil
	}
	var out []driveInfo
	for i := 0; i < 26; i++ {
		if mask&(1<<uint(i)) == 0 {
			continue
		}
		letter := string(rune('A' + i))
		root := letter + `:\`
		if _, err := os.Stat(root); err != nil {
			continue
		}
		out = append(out, driveInfo{letter: letter, label: volumeLabel(root)})
	}
	return out
}

// volumeLabel is a drive's label, "" when it has none or will not say.
func volumeLabel(root string) string {
	p, err := windows.UTF16PtrFromString(root)
	if err != nil {
		return ""
	}
	buf := make([]uint16, windows.MAX_PATH+1)
	if err := windows.GetVolumeInformation(p, &buf[0], uint32(len(buf)), nil, nil, nil, nil, 0); err != nil {
		return ""
	}
	return windows.UTF16ToString(buf)
}

// hiddenInfo is the attribute half of the hidden rule: Explorer does not
// list a hidden or system entry, and neither does the chooser.
func hiddenInfo(fi os.FileInfo) bool {
	if a, ok := fi.Sys().(*syscall.Win32FileAttributeData); ok {
		return a.FileAttributes&(syscall.FILE_ATTRIBUTE_HIDDEN|syscall.FILE_ATTRIBUTE_SYSTEM) != 0
	}
	return false
}
