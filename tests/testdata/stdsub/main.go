// Command stdsub reads global flags with flag.Parse and gives each
// subcommand its own flag.FlagSet, so flags after the subcommand name are
// the subcommand's business.
package main

import (
	"flag"
	"fmt"
	"os"
)

func main() {
	verbose := flag.Bool("verbose", false, "")
	flag.Parse()
	if flag.NArg() > 0 && flag.Arg(0) == "serve" {
		fs := flag.NewFlagSet("serve", flag.ExitOnError)
		port := fs.Int("port", 8080, "")
		fs.Parse(flag.Args()[1:])
		fmt.Println("serve", *verbose, *port)
		return
	}
	fmt.Println("usage: stdsub [-verbose] serve [-port N]")
	os.Exit(2)
}
