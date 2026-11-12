package main

import "net"

func pflagNet() net.IPNet {
	_, n, _ := net.ParseCIDR("10.0.0.0/8")
	return *n
}
