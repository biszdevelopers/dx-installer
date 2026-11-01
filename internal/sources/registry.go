package sources

import (
	"errors"
	"fmt"
	"net/url"
	"strings"

	"developers-club-dx/internal/domain/tool"
)

type RegistryURLs map[tool.RegistryEcosystem]string

func DefaultRegistry(ecosystem tool.RegistryEcosystem) string {
	switch ecosystem {
	case tool.NPMRegistry:
		return "https://registry.npmjs.org/"
	case tool.PyPIRegistry:
		return "https://pypi.org/simple"
	case tool.GoModuleRegistry:
		return "https://proxy.golang.org"
	default:
		return ""
	}
}

func ValidateRegistryURL(ecosystem tool.RegistryEcosystem, value string) error {
	if DefaultRegistry(ecosystem) == "" {
		return fmt.Errorf("unsupported registry ecosystem %q", ecosystem)
	}
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("registry URL must use HTTPS and contain no credentials, query, or fragment")
	}
	if strings.ContainsAny(value, "\r\n\x00") {
		return errors.New("registry URL contains a control character")
	}
	return nil
}

func ParseRegistryAssignments(entries []string) (RegistryURLs, error) {
	registries := make(RegistryURLs, len(entries))
	for _, entry := range entries {
		ecosystemText, value, found := strings.Cut(entry, "=")
		ecosystem := tool.RegistryEcosystem(strings.TrimSpace(ecosystemText))
		value = strings.TrimSpace(value)
		if !found || ecosystem == "" || value == "" {
			return nil, errors.New("registry must use ecosystem=https://registry.example")
		}
		if _, duplicate := registries[ecosystem]; duplicate {
			return nil, fmt.Errorf("registry for %q was supplied more than once", ecosystem)
		}
		if err := ValidateRegistryURL(ecosystem, value); err != nil {
			return nil, fmt.Errorf("registry for %q: %w", ecosystem, err)
		}
		registries[ecosystem] = strings.TrimRight(value, "/")
	}
	return registries, nil
}
