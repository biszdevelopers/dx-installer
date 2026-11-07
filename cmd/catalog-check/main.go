package main

import (
	"flag"
	"fmt"
	"os"
	"slices"
	"strings"

	"developers-club-dx/internal/catalog"
	"developers-club-dx/internal/domain/tool"
)

func main() {
	showMatrix := flag.Bool("matrix", false, "report exact install and native-alternative coverage for Windows, macOS, and Linux")
	flag.Parse()
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
	if *showMatrix {
		for _, operatingSystem := range []tool.OperatingSystem{tool.Windows, tool.MacOS, tool.Linux} {
			for _, architecture := range []tool.Architecture{tool.AMD64, tool.ARM64} {
				var direct, alternatives int
				var unavailable []string
				for _, manifest := range items.Tools() {
					if manifest.Supports(operatingSystem, architecture) {
						direct++
						continue
					}
					coveredByAlternative := false
					for _, alternative := range manifest.PlatformAlternatives[operatingSystem] {
						if len(alternative.Architectures) > 0 && !slices.Contains(alternative.Architectures, architecture) {
							continue
						}
						candidate, exists := items.Tool(alternative.ToolID)
						if exists && candidate.Supports(operatingSystem, architecture) {
							coveredByAlternative = true
							break
						}
					}
					if coveredByAlternative {
						alternatives++
					} else {
						unavailable = append(unavailable, manifest.ID)
					}
				}
				fmt.Printf("%s/%s: %d direct, %d native alternatives, %d unavailable\n", operatingSystem, architecture, direct, alternatives, len(unavailable))
				if len(unavailable) > 0 {
					fmt.Printf("  Missing: %s\n", strings.Join(unavailable, ", "))
				}
			}
		}
	}
}
