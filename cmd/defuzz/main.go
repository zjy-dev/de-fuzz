package main

import (
	"github.com/zjy-dev/de-fuzz/cmd/defuzz/app"
	"fmt"
	"os"
)

func main() {
	if err := app.NewDefuzzCommand().Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}
