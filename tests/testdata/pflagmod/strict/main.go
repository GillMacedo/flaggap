// Command strict turns off interspersed flags, so pflag stops at the
// first positional argument like the flag package does.
package main

import (
	"fmt"

	"github.com/spf13/pflag"
)

func main() {
	port := pflag.Int("port", 80, "")
	debug := pflag.Bool("debug", false, "")
	pflag.CommandLine.SetInterspersed(false)
	pflag.Parse()
	fmt.Println(*port, *debug, pflag.Args())
}
