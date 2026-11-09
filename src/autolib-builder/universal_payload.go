package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Known binary formats are a rejection check, not proof that other files are portable.
func validateUniversalPayload(installRoot string) error {
	return filepath.WalkDir(installRoot, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			_, err := safePackageLink(installRoot, path)
			return err // The regular target is visited separately inside this tree.
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("universal payload must be a regular file: %s", path)
		}
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		defer file.Close()
		info, err := file.Stat()
		if err != nil {
			return err
		}
		if format := targetBinaryFormat(file, info.Size()); format != "" {
			return fmt.Errorf("universal payload %s contains %s target binary content", path, format)
		}
		return nil
	})
}

func targetBinaryFormat(input io.ReaderAt, size int64) string {
	var header [64]byte
	n, _ := input.ReadAt(header[:], 0)
	data := header[:n]
	if bytes.HasPrefix(data, []byte("\x7fELF")) {
		return "ELF"
	}
	if n >= 4 {
		for _, order := range []binary.ByteOrder{binary.BigEndian, binary.LittleEndian} {
			switch order.Uint32(data) {
			case 0xfeedface, 0xfeedfacf:
				return "Mach-O"
			case 0xcafebabe, 0xcafebabf:
				// Java class files share the fat magic, but have a class version here.
				if n >= 12 && order.Uint32(data[4:]) > 0 && order.Uint32(data[4:]) < 100 && machoCPU(order.Uint32(data[8:])) {
					return "fat Mach-O"
				}
			}
		}
	}
	if n >= 64 && bytes.HasPrefix(data, []byte("MZ")) {
		var signature [4]byte
		if _, err := input.ReadAt(signature[:], int64(binary.LittleEndian.Uint32(data[60:]))); err == nil && string(signature[:]) == "PE\x00\x00" {
			return "PE"
		}
	}
	if n >= 20 {
		machine := binary.LittleEndian.Uint16(data)
		sections := binary.LittleEndian.Uint16(data[2:])
		if coffMachine(machine) && sections > 0 && binary.LittleEndian.Uint16(data[16:]) == 0 {
			return "COFF object"
		}
		if machine == 0 && sections == 0xffff && coffMachine(binary.LittleEndian.Uint16(data[6:])) {
			return "COFF import/big object"
		}
	}
	if bytes.HasPrefix(data, []byte("!<thin>\n")) {
		return "thin target archive"
	}
	if bytes.HasPrefix(data, []byte("!<arch>\n")) {
		// Inspect members, so a generic ar containing text is not classified by name.
		for offset := int64(8); offset+60 <= size; {
			var member [60]byte
			if _, err := input.ReadAt(member[:], offset); err != nil || string(member[58:]) != "`\n" {
				break
			}
			length, err := strconv.ParseInt(strings.TrimSpace(string(member[48:58])), 10, 64)
			start := offset + 60
			if err != nil || length < 0 || length > size-start {
				break
			}
			name := strings.TrimSpace(string(member[:16]))
			if strings.HasPrefix(name, "#1/") {
				nameSize, err := strconv.ParseInt(strings.TrimPrefix(name, "#1/"), 10, 64)
				if err != nil || nameSize < 0 || nameSize > length {
					break
				}
				start += nameSize
				length -= nameSize
			}
			if format := targetBinaryFormat(io.NewSectionReader(input, start, length), length); format != "" {
				return "archive containing " + format
			}
			memberLength, _ := strconv.ParseInt(strings.TrimSpace(string(member[48:58])), 10, 64)
			offset += 60 + memberLength + memberLength%2
		}
	}
	return ""
}

func machoCPU(cpu uint32) bool {
	switch cpu & 0x00ffffff {
	case 7, 12, 18:
		return true
	}
	return false
}
func coffMachine(machine uint16) bool {
	switch machine {
	case 0x014c, 0x8664, 0xaa64, 0x01c0, 0x01c2, 0x01c4, 0x01f0, 0x01f1, 0x5032, 0x5064, 0x5128:
		return true
	}
	return false
}
