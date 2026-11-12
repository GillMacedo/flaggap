// Command initpanic panics before it parses its flags.
package main

import "flag"

var port = flag.Int("port", 80, "")

func init() { panic("config file missing") }

func main() { flag.Parse() }
