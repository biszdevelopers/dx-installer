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
	platforms := 0
	for _, manifest := range items.Tools() {
		for _, architectures := range manifest.Platforms {
			platforms += len(architectures)
		}
	}
	fmt.Printf("Validated %d tools, %d profiles, and %d platform/architecture install paths.\n", len(items.Tools()), len(items.Profiles()), platforms)
}
