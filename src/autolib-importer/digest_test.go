package main

import (
	"strings"
	"testing"
)

func TestPackageDigestWhitespace(t *testing.T) {
	digest := strings.Repeat("0123456789abcdef", 4)
	for _, tc := range []struct {
		name, text string
		valid      bool
	}{
		{"bare", digest, true},
		{"lf", digest + "\n", true},
		{"crlf", digest + "\r\n", true},
		{"padded", " \t\r\n" + digest + "\r\n\t ", true},
		{"empty", " \t\r\n", false},
		{"short", digest[:63], false},
		{"uppercase", strings.ToUpper(digest), false},
		{"embedded space", digest[:32] + " " + digest[32:], false},
		{"embedded newline", digest[:32] + "\n" + digest[32:], false},
		{"multiple digests", digest + "\n" + digest, false},
		{"junk", "x" + digest, false},
		{"nul", digest + "\x00", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parsePackageDigest("digest.sha256", []byte(tc.text))
			if (err == nil) != tc.valid || (tc.valid && got != digest) {
				t.Fatalf("parse digest: got %q, error %v; valid=%t", got, err, tc.valid)
			}
		})
	}
}
