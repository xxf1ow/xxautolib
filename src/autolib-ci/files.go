package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
)

type ciSlot struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	OS      string `json:"os"`
	Arch    string `json:"arch"`
}

var commitPattern = regexp.MustCompile(`^[0-9a-fA-F]{40}$`)

func decodeStrictJSON(reader io.Reader, result any) error {
	decoder := json.NewDecoder(io.LimitReader(reader, 8<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(result); err != nil {
		return err
	}
	var extra any
	if !errors.Is(decoder.Decode(&extra), io.EOF) {
		return fmt.Errorf("trailing JSON data")
	}
	return nil
}

func decodeStrictJSONFile(filename string, result any) error {
	file, err := os.Open(filename)
	if err != nil {
		return err
	}
	defer file.Close()
	return decodeStrictJSON(file, result)
}
