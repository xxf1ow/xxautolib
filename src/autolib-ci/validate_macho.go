package main

import (
	"bytes"
	"debug/macho"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func darwinSystemPath(path string) bool {
	return strings.HasPrefix(path, "/usr/lib/") || strings.HasPrefix(path, "/System/Library/")
}

func darwinSDKLinkInput(path, sdkRoot string) bool {
	if !filepath.IsAbs(sdkRoot) || !strings.HasSuffix(path, ".tbd") {
		return false
	}
	sdk, err := filepath.EvalSymlinks(sdkRoot)
	if err != nil {
		return false
	}
	actual, err := filepath.EvalSymlinks(path)
	if err != nil || !strings.HasSuffix(actual, ".tbd") {
		return false
	}
	relative, err := filepath.Rel(sdk, actual)
	return err == nil && darwinSystemPath("/"+filepath.ToSlash(relative))
}

func darwinVersion(value string) (uint32, error) {
	parts := strings.Split(value, ".")
	if len(parts) < 2 || len(parts) > 3 {
		return 0, fmt.Errorf("invalid macOS version %q", value)
	}
	var result uint32
	for i, part := range parts {
		n, err := strconv.ParseUint(part, 10, 16)
		if err != nil || i > 0 && n > 255 {
			return 0, fmt.Errorf("invalid macOS version %q", value)
		}
		result |= uint32(n) << uint(16-i*8)
	}
	return result, nil
}

func inspectDarwinMachO(m *macho.File, target string, importer bool) error {
	if m.Cpu != macho.CpuArm64 {
		return fmt.Errorf("Mach-O requires ARM64, got %v", m.Cpu)
	}
	max, err := darwinVersion(target)
	if err != nil {
		return err
	}
	versionFound := false
	for _, load := range m.Loads {
		raw := load.Raw()
		if len(raw) < 8 {
			return fmt.Errorf("invalid Mach-O load command")
		}
		cmd := m.ByteOrder.Uint32(raw[:4])
		var version uint32
		switch cmd {
		case 0x32:
			if len(raw) < 24 || m.ByteOrder.Uint32(raw[8:12]) != 1 {
				return fmt.Errorf("Mach-O requires macOS build version")
			}
			version = m.ByteOrder.Uint32(raw[12:16])
		case 0x24:
			if len(raw) < 16 {
				return fmt.Errorf("invalid macOS deployment command")
			}
			version = m.ByteOrder.Uint32(raw[8:12])
		default:
			continue
		}
		if version > max {
			return fmt.Errorf("Mach-O deployment %d.%d.%d exceeds %s", version>>16, version>>8&255, version&255, target)
		}
		versionFound = true
	}
	if !versionFound {
		return fmt.Errorf("Mach-O lacks a macOS deployment target")
	}
	libraries, err := m.ImportedLibraries()
	if err != nil {
		return err
	}
	for _, lib := range libraries {
		if importer && (strings.Contains(lib, "libc++") || strings.Contains(lib, "libstdc++") || strings.Contains(lib, "libgcc")) {
			return fmt.Errorf("Go importer depends on C++ runtime %s", lib)
		}
		if darwinSystemPath(lib) {
			continue
		}
		if !importer && (strings.HasPrefix(lib, "@rpath/") || strings.HasPrefix(lib, "@loader_path/") || strings.HasPrefix(lib, "@executable_path/")) {
			continue
		}
		return fmt.Errorf("Mach-O non-system dynamic dependency %s", lib)
	}
	return nil
}

func validateDarwinBinary(path string, deploymentTarget string, importer bool) error {
	m, err := macho.Open(path)
	if err != nil {
		return fmt.Errorf("inspect Mach-O %s: %w", path, err)
	}
	defer m.Close()
	if err := inspectDarwinMachO(m, deploymentTarget, importer); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

func validateDarwinArchive(path, target string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if !bytes.HasPrefix(data, []byte("!<arch>\n")) {
		return fmt.Errorf("invalid static archive %s", path)
	}
	count := 0
	for offset := 8; offset < len(data); {
		if offset+60 > len(data) || string(data[offset+58:offset+60]) != "`\n" {
			return fmt.Errorf("invalid archive member in %s", path)
		}
		header := data[offset : offset+60]
		size, err := strconv.Atoi(strings.TrimSpace(string(header[48:58])))
		if err != nil || size < 0 || size > len(data)-offset-60 {
			return fmt.Errorf("invalid archive member size in %s", path)
		}
		name := strings.TrimSpace(string(header[:16]))
		body := data[offset+60 : offset+60+size]
		if strings.HasPrefix(name, "#1/") {
			n, err := strconv.Atoi(strings.TrimPrefix(name, "#1/"))
			if err != nil || n < 0 || n > len(body) {
				return fmt.Errorf("invalid BSD archive name")
			}
			name = strings.TrimRight(string(body[:n]), "\x00")
			body = body[n:]
		}
		if !strings.HasPrefix(name, "__.SYMDEF") && name != "/" && name != "//" {
			m, err := macho.NewFile(bytes.NewReader(body))
			if err != nil {
				return fmt.Errorf("archive %s member %s: %w", path, name, err)
			}
			err = inspectDarwinMachO(m, target, false)
			m.Close()
			if err != nil {
				return fmt.Errorf("archive %s member %s: %w", path, name, err)
			}
			count++
		}
		offset += 60 + size
		if offset%2 != 0 {
			offset++
		}
	}
	if count == 0 {
		return fmt.Errorf("static archive has no Mach-O members: %s", path)
	}
	return nil
}

func verifyDarwinPackageInputs(runRoot string, packageRoots []string, deploymentTarget, sdkRoot string) error {
	consumer := filepath.Join(runRoot, "build", "autolib_smoke")
	if err := validateDarwinBinary(consumer, deploymentTarget, false); err != nil {
		return err
	}
	inputs, err := os.ReadFile(filepath.Join(runRoot, "build/link-inputs-Release.txt"))
	if err != nil {
		return err
	}
	for _, input := range strings.Split(strings.TrimSpace(string(inputs)), "\n") {
		input = strings.TrimSpace(input)
		if input == "" || darwinSystemPath(input) || darwinSDKLinkInput(input, sdkRoot) {
			continue
		}
		if err := requireCIPathInPackages(packageRoots, input, "Darwin consumer link input"); err != nil {
			return err
		}
		actual, err := filepath.EvalSymlinks(input)
		if err != nil {
			return err
		}
		if err := requireCIPathInPackages(packageRoots, actual, "Darwin consumer link input"); err != nil {
			return err
		}
	}
	binaries := []string{consumer}
	for _, root := range packageRoots {
		err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
			if err != nil || entry.IsDir() {
				return err
			}
			if entry.Type()&os.ModeSymlink != 0 {
				actual, err := filepath.EvalSymlinks(path)
				if err != nil {
					return err
				}
				return requireCIPathInPackages(packageRoots, actual, "Darwin package symlink")
			}
			if strings.HasSuffix(path, ".a") {
				return validateDarwinArchive(path, deploymentTarget)
			}
			if strings.HasSuffix(path, ".dylib") {
				binaries = append(binaries, path)
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	exe, err := macho.Open(consumer)
	if err != nil {
		return err
	}
	defer exe.Close()
	rpaths := func(m *macho.File) []string {
		var result []string
		for _, load := range m.Loads {
			if r, ok := load.(*macho.Rpath); ok {
				result = append(result, r.Path)
			}
		}
		return result
	}
	expand := func(value, binary string) string {
		value = strings.ReplaceAll(value, "@loader_path", filepath.Dir(binary))
		return strings.ReplaceAll(value, "@executable_path", filepath.Dir(consumer))
	}
	for _, path := range binaries {
		m, err := macho.Open(path)
		if err != nil {
			return err
		}
		if err := inspectDarwinMachO(m, deploymentTarget, false); err != nil {
			m.Close()
			return fmt.Errorf("%s: %w", path, err)
		}
		paths := rpaths(m)
		if path != consumer {
			paths = append(paths, rpaths(exe)...)
		}
		for _, r := range paths {
			actual := expand(r, path)
			if !filepath.IsAbs(actual) {
				m.Close()
				return fmt.Errorf("unresolved Darwin rpath %s", r)
			}
			if err := requireCIPathInPackages(packageRoots, actual, "Darwin rpath"); err != nil {
				m.Close()
				return err
			}
		}
		libs, err := m.ImportedLibraries()
		m.Close()
		if err != nil {
			return err
		}
		for _, lib := range libs {
			if darwinSystemPath(lib) {
				continue
			}
			candidates := []string{expand(lib, path)}
			if strings.HasPrefix(lib, "@rpath/") {
				candidates = nil
				for _, r := range paths {
					candidates = append(candidates, filepath.Join(expand(r, path), strings.TrimPrefix(lib, "@rpath/")))
				}
			}
			found := false
			for _, candidate := range candidates {
				actual, err := filepath.EvalSymlinks(candidate)
				if os.IsNotExist(err) {
					continue
				}
				if err != nil {
					return err
				}
				if err := requireCIPathInPackages(packageRoots, actual, "Darwin loaded dependency"); err != nil {
					return err
				}
				found = true
				break
			}
			if !found {
				return fmt.Errorf("unresolved Darwin dependency %s in %s", lib, path)
			}
		}
	}
	report := fmt.Sprintf("architecture=arm64\ndeployment_max=%s\nconsumer=%s\nlink-inputs:\n%s", deploymentTarget, consumer, inputs)
	return os.WriteFile(filepath.Join(runRoot, "macho-evidence.txt"), []byte(report), 0644)
}
