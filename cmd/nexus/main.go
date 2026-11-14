// Command nexus is the Knowhere Nexus server.
//
// Subcommands:
//
//	nexus serve -config <path>        run the server
//	nexus healthcheck [-addr <h:p>]   probe /healthz (used by the container HEALTHCHECK)
//	nexus version                     print build information
package main

import (
	"fmt"
	"io"
	"os"

	"github.com/ntsrihari/knowhere-nexus/internal/buildinfo"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

const usage = `usage: nexus <command> [flags]

commands:
  serve        run the Nexus server (-config <path>)
  healthcheck  probe the local /healthz endpoint (-addr <host:port>)
  version      print build information
`

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	switch args[0] {
	case "serve":
		return serveCmd(args[1:], stderr)
	case "healthcheck":
		return healthcheckCmd(args[1:], stderr)
	case "version":
		fmt.Fprintln(stdout, buildinfo.String())
		return 0
	case "-h", "--help", "help":
		fmt.Fprint(stdout, usage)
		return 0
	default:
		fmt.Fprintf(stderr, "unknown command %q\n\n%s", args[0], usage)
		return 2
	}
}
