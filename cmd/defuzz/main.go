package main

import (
	"fmt"
	"os"

	"github.com/zjy-dev/de-fuzz/cmd/defuzz/app"
	_ "github.com/zjy-dev/de-fuzz/internal/oracle/plugins" // Register oracle plugins
)

func main() {
	if err := app.NewDefuzzCommand().Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}
