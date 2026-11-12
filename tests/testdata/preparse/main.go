// Command preparse reads -config from its first argument with one flag set
// before it parses the rest with flag.Parse.
package main

import (
	"flag"
	"fmt"
	"os"
)

func main() {
	pre := flag.NewFlagSet("pre", flag.ContinueOnError)
	config := pre.String("config", "", "")
	if len(os.Args) > 1 {
		pre.Parse(os.Args[1:2])
	}
	port := flag.Int("port", 80, "")
	flag.CommandLine.Parse(os.Args[2:])
	fmt.Println(*config, *port)
}
