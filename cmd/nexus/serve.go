package main

import (
	"fmt"
	"io"
)

// serveCmd is wired to the application in M1-10.
func serveCmd(args []string, stderr io.Writer) int {
	fmt.Fprintln(stderr, "serve: not wired yet")
	return 1
}
