// Command cachedir prints os.UserCacheDir(), for dev scripts that need the
// same folder trout looks in.
package main

import (
	"fmt"
	"os"
)

func main() {
	dir, err := os.UserCacheDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println(dir)
}
