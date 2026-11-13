//go:build !windows

package main

// openUserInstall solo hace algo en Windows (redirect_windows.go).
func openUserInstall() bool { return false }
