//go:build !unix

package main

import "os"

// chown does nothing: trout-cert runs on Linux servers.
func chown(string, os.FileInfo) error { return nil }
