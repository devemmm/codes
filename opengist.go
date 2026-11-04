package main

import (
	"github.com/devemmm/codes/internal/cli"
	"os"
)

func main() {
	if err := cli.App(); err != nil {
		os.Exit(1)
	}
}
