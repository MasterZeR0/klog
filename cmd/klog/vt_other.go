//go:build !windows

package main

import "os"

// enableVT reports whether f renders ANSI escapes; unix terminals always do.
func enableVT(*os.File) bool { return true }
