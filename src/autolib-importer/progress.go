package main

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

type downloadProgress struct {
	output                      io.Writer
	ref                         string
	terminal                    bool
	total, downloaded, reported int64
	updated                     time.Time
}

func newDownloadProgress(output io.Writer, ref string) *downloadProgress {
	if output == nil {
		output = io.Discard
	}
	p := &downloadProgress{output: output, ref: ref}
	if file, ok := output.(*os.File); ok {
		if info, err := file.Stat(); err == nil {
			p.terminal = info.Mode()&os.ModeCharDevice != 0
		}
	}
	return p
}

func (p *downloadProgress) report() {
	prefix, suffix := "-- ", "\n"
	if p.terminal {
		prefix, suffix = "\r-- ", ""
	}
	const mib = 1024 * 1024
	if p.total > 0 {
		percent := min(100, p.downloaded*100/p.total)
		filled := int(percent / 10)
		fmt.Fprintf(p.output, "%s[AutoLib] Downloading %s: [%s%s] %d%%  %.1f/%.1f MiB%s", prefix, p.ref,
			strings.Repeat("=", filled), strings.Repeat("-", 10-filled), percent,
			float64(p.downloaded)/mib, float64(p.total)/mib, suffix)
	} else {
		fmt.Fprintf(p.output, "%s[AutoLib] Downloading %s: %.1f MiB%s", prefix, p.ref, float64(p.downloaded)/mib, suffix)
	}
	p.reported, p.updated = p.downloaded, time.Now()
}

func (p *downloadProgress) Write(data []byte) (int, error) {
	p.downloaded += int64(len(data))
	interval := time.Second
	if p.terminal {
		interval = 200 * time.Millisecond
	}
	// Report the first bytes immediately, then throttle redirected logs as well as terminal refreshes.
	if p.reported == 0 || time.Since(p.updated) >= interval {
		p.report()
	}
	return len(data), nil
}

func (p *downloadProgress) finish() {
	if p.downloaded != p.reported {
		p.report()
	}
	if p.terminal {
		fmt.Fprintln(p.output)
	}
}
