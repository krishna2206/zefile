// Command zefile is a self-hosted file server.
package main

import (
	"flag"
	"fmt"
)

// version is overridden at build time with -ldflags.
var version = "dev"

func main() {
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Printf("zefile %s\n", version)
		return
	}

	fmt.Printf("zefile %s — nothing to serve yet, see docs/roadmap.html\n", version)
}
