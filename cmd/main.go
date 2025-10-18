// cmd/main.go
package main

import (
	"github.com/iMithrellas/tarragon/internal/cli"
	"log"
)

func main() {
	// Journalctl already records timestamps; keep logs clean.
	log.SetFlags(0)
	cli.Execute()
}
