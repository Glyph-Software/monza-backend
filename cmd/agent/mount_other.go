//go:build !linux

package main

import "log"

func mountFS() {
	log.Println("monza-agent: skipping filesystem mounts (not Linux)")
}
