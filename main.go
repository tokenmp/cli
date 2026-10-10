// Command tokenmp is the TokenMP command line interface.
package main

import (
	"context"
	"os"

	"github.com/tokenmp/cli/cmd"
)

func main() {
	// fang reports the error in a styled form before Execute returns.
	if err := cmd.Execute(context.Background()); err != nil {
		os.Exit(1)
	}
}
