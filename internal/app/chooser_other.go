//go:build !windows

package app

import "os"

// listDrives is Windows' drive letters; elsewhere the top level is the home
// folder and / (chooserRoots), so there are none.
func listDrives() []driveInfo { return nil }

// hiddenInfo is Windows' hidden and system attributes; elsewhere a leading
// dot (hiddenName) is the whole rule.
func hiddenInfo(os.FileInfo) bool { return false }
