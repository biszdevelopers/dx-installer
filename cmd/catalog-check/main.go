package main

import (
	"fmt"
	"os"

	"developers-club-dx/internal/catalog"
)

func main() {
	items, err := catalog.LoadEmbedded()
	if err != nil {
		fmt.Fprintln(os.Stderr, "catalog-check:", err)
		os.Exit(1)
	}
	fmt.Printf("Validated %d tool manifests and %d profiles.\n", len(items.Tools()), len(items.Profiles()))
}
