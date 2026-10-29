//go:build !darwin && !linux

package main

func sockets() string        { return "n/a" }
func rawSockets() string     { return "" }
func timeWaitReport() string { return "n/a" }
