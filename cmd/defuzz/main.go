package main

import (
	"defuzz/cmd/defuzz/app"
	"fmt"
	"os"
)

func main() {
	if err := app.NewDefuzzCommand().Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}
