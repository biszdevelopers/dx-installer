package detection

import (
	"debug/elf"
	"debug/macho"
	"debug/pe"
	"fmt"
	"os"
	"sort"
	"strings"

	"developers-club-dx/internal/domain/tool"
)

func binaryArchitectures(path string) ([]tool.Architecture, bool, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, false, err
	}
	var signature [4]byte
	_, readErr := file.Read(signature[:])
	_ = file.Close()
	if readErr != nil {
		return nil, false, readErr
	}
	switch {
	case string(signature[:]) == "\x7fELF":
		binary, err := elf.Open(path)
		if err != nil {
			return nil, true, err
		}
		defer binary.Close()
		return architectureForELF(binary.Machine), true, nil
	case string(signature[:2]) == "MZ":
		binary, err := pe.Open(path)
		if err != nil {
			return nil, true, err
		}
		defer binary.Close()
		return architectureForPE(binary.Machine), true, nil
	case isMachOMagic(signature):
		if isFatMachOMagic(signature) {
			binary, err := macho.OpenFat(path)
			if err != nil {
				return nil, true, err
			}
			defer binary.Close()
			architectures := make([]tool.Architecture, 0, len(binary.Arches))
			for _, architecture := range binary.Arches {
				architectures = append(architectures, architectureForMachO(architecture.Cpu)...)
			}
			return uniqueArchitectures(architectures), true, nil
		}
		binary, err := macho.Open(path)
		if err != nil {
			return nil, true, err
		}
		defer binary.Close()
		return architectureForMachO(binary.Cpu), true, nil
	default:
		return nil, false, nil
	}
}

func architectureForELF(machine elf.Machine) []tool.Architecture {
	switch machine {
	case elf.EM_X86_64:
		return []tool.Architecture{tool.AMD64}
	case elf.EM_AARCH64:
		return []tool.Architecture{tool.ARM64}
	case elf.EM_386:
		return []tool.Architecture{tool.I386}
	case elf.EM_ARM:
		return []tool.Architecture{tool.ARM}
	case elf.EM_RISCV:
		return []tool.Architecture{tool.RISCV64}
	default:
		return nil
	}
}

func architectureForPE(machine uint16) []tool.Architecture {
	switch machine {
	case pe.IMAGE_FILE_MACHINE_AMD64:
		return []tool.Architecture{tool.AMD64}
	case pe.IMAGE_FILE_MACHINE_ARM64:
		return []tool.Architecture{tool.ARM64}
	case pe.IMAGE_FILE_MACHINE_I386:
		return []tool.Architecture{tool.I386}
	case pe.IMAGE_FILE_MACHINE_ARMNT:
		return []tool.Architecture{tool.ARM}
	default:
		return nil
	}
}

func architectureForMachO(cpu macho.Cpu) []tool.Architecture {
	switch cpu {
	case macho.CpuAmd64:
		return []tool.Architecture{tool.AMD64}
	case macho.CpuArm64:
		return []tool.Architecture{tool.ARM64}
	case macho.Cpu386:
		return []tool.Architecture{tool.I386}
	case macho.CpuArm:
		return []tool.Architecture{tool.ARM}
	default:
		return nil
	}
}

func isMachOMagic(signature [4]byte) bool {
	magic := string(signature[:])
	switch magic {
	case "\xfe\xed\xfa\xce", "\xce\xfa\xed\xfe", "\xfe\xed\xfa\xcf", "\xcf\xfa\xed\xfe",
		"\xca\xfe\xba\xbe", "\xbe\xba\xfe\xca", "\xca\xfe\xba\xbf", "\xbf\xba\xfe\xca":
		return true
	default:
		return false
	}
}

func isFatMachOMagic(signature [4]byte) bool {
	magic := string(signature[:])
	return magic == "\xca\xfe\xba\xbe" || magic == "\xbe\xba\xfe\xca" || magic == "\xca\xfe\xba\xbf" || magic == "\xbf\xba\xfe\xca"
}

func uniqueArchitectures(architectures []tool.Architecture) []tool.Architecture {
	seen := make(map[tool.Architecture]bool, len(architectures))
	unique := make([]tool.Architecture, 0, len(architectures))
	for _, architecture := range architectures {
		if architecture == "" || seen[architecture] {
			continue
		}
		seen[architecture] = true
		unique = append(unique, architecture)
	}
	sort.Slice(unique, func(i, j int) bool { return unique[i] < unique[j] })
	return unique
}

func containsArchitecture(architectures []tool.Architecture, expected tool.Architecture) bool {
	for _, architecture := range architectures {
		if architecture == expected {
			return true
		}
	}
	return false
}

func joinArchitectures(architectures []tool.Architecture) string {
	if len(architectures) == 0 {
		return "unknown"
	}
	values := make([]string, len(architectures))
	for index, architecture := range architectures {
		values[index] = string(architecture)
	}
	return strings.Join(values, ", ")
}

func architectureDiagnostic(path string, architectures []tool.Architecture) error {
	if len(architectures) == 0 {
		return fmt.Errorf("could not identify architecture for %s", path)
	}
	return nil
}
