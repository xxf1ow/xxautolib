package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
)

type ciSlot struct {
	Name string `json:"name"`
	OS   string `json:"os"`
	Arch string `json:"arch"`
}

var sha256Pattern = regexp.MustCompile(`^[0-9a-fA-F]{64}$`)
var commitPattern = regexp.MustCompile(`^[0-9a-fA-F]{40}$`)

func ciPackageAssetName(slot ciSlot) string {
	return slot.Name + "-" + slot.OS + "-" + slot.Arch + ".tar.zst"
}

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

func validGitHubDigest(value string) bool {
	return strings.HasPrefix(value, "sha256:") && sha256Pattern.MatchString(strings.TrimPrefix(value, "sha256:"))
}
func digestMatches(apiDigest, digest string) bool {
	return validGitHubDigest(apiDigest) && sha256Pattern.MatchString(digest) && strings.EqualFold(strings.TrimPrefix(apiDigest, "sha256:"), digest)
}
func hashReader(reader io.Reader) (string, error) {
	hash := sha256.New()
	if _, err := io.Copy(hash, reader); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func copyCIFile(source, destination string) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(output, input)
	return errors.Join(copyErr, output.Close())
}
