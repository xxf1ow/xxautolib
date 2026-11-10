package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode"
)

type releaseAsset struct {
	Name, SHA256 string
	Size         int64
	URLs         []string
}

func validateReleaseHTTPS(address string) error {
	u, err := url.Parse(address)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || strings.ContainsFunc(address, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) {
		return fmt.Errorf("Release requests require HTTPS without credentials")
	}
	return nil
}

func releaseGET(ctx context.Context, configured *http.Client, address string) (*http.Response, error) {
	if err := validateReleaseHTTPS(address); err != nil {
		return nil, err
	}
	client := http.Client{}
	if configured != nil {
		client = *configured
	}
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if err := validateReleaseHTTPS(req.URL.String()); err != nil {
			return err
		}
		if len(via) >= 10 {
			return fmt.Errorf("too many Release redirects")
		}
		return nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/octet-stream")
	req.Header.Set("User-Agent", "xxautolib-importer")
	response, err := client.Do(req)
	if err != nil {
		if requestError, ok := err.(*url.Error); ok {
			err = requestError.Err
		}
		return nil, fmt.Errorf("Release request: %w", err)
	}
	if response.StatusCode != http.StatusOK {
		response.Body.Close()
		return nil, fmt.Errorf("Release request: HTTP %d", response.StatusCode)
	}
	return response, nil
}

type downloadFile interface {
	io.Writer
	io.Closer
	Name() string
}

type downloadFiles struct {
	create func(string) (downloadFile, error)
	remove func(string) error
	rename func(string, string) error
}

func defaultDownloadFiles() downloadFiles {
	return downloadFiles{
		create: func(dir string) (downloadFile, error) { return os.CreateTemp(dir, ".download-") },
		remove: os.Remove, rename: os.Rename,
	}
}

func downloadReleaseAsset(ctx context.Context, client *http.Client, asset releaseAsset, destination string, progress *downloadProgress) error {
	return downloadAsset(ctx, client, asset, destination, progress, defaultDownloadFiles())
}

type downloadTimeouts struct{ Connect, TLS, Header, Idle, Total time.Duration }

var defaultDownloadTimeouts = downloadTimeouts{30 * time.Second, 30 * time.Second, 30 * time.Second, 60 * time.Second, time.Hour}

func downloadAsset(ctx context.Context, client *http.Client, asset releaseAsset, destination string, progress *downloadProgress, files downloadFiles) error {
	return downloadAssetWithTimeouts(ctx, client, asset, destination, progress, files, defaultDownloadTimeouts)
}

func downloadAssetWithTimeouts(ctx context.Context, configured *http.Client, asset releaseAsset, destination string, progress *downloadProgress, files downloadFiles, limits downloadTimeouts) error {
	client := &http.Client{}
	if configured != nil {
		*client = *configured
	}
	client.Timeout = 0
	base := client.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	if transport, ok := base.(*http.Transport); ok {
		transport = transport.Clone()
		dial := transport.DialContext
		if dial == nil {
			dial = (&net.Dialer{}).DialContext
		}
		transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
			dialCtx, cancel := context.WithTimeout(ctx, limits.Connect)
			defer cancel()
			return dial(dialCtx, network, address)
		}
		transport.TLSHandshakeTimeout = limits.TLS
		transport.ResponseHeaderTimeout = limits.Header
		client.Transport = transport
		defer transport.CloseIdleConnections()
	}

	if err := validateDeliveryAsset(asset); err != nil {
		return err
	}
	if _, err := os.Lstat(destination); err == nil {
		return fmt.Errorf("download destination already exists: %s", destination)
	} else if !os.IsNotExist(err) {
		return err
	}
	var failures []error
	for index, address := range asset.URLs {
		if err := ctx.Err(); err != nil {
			return err
		}
		file, err := files.create(filepath.Dir(destination))
		if err != nil {
			return err
		}
		progress.startSource(index+1, len(asset.URLs), address, asset.Size)
		sourceCtx, cancel := context.WithTimeout(ctx, limits.Total)
		sourceErr, localErr := receiveReleaseAsset(sourceCtx, client, address, asset, file, progress, limits.Idle)
		cancel()
		closeErr := file.Close()
		if sourceErr != nil || localErr != nil || closeErr != nil {
			removeErr := files.remove(file.Name())
			if err := errors.Join(localErr, closeErr, removeErr); err != nil {
				return err
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			sourceErr = safeDownloadError(sourceErr)
			progress.sourceFailed(index+1, address, sourceErr)
			host, _ := url.Parse(address)
			failures = append(failures, fmt.Errorf("source %d (%s): %w", index+1, host.Host, sourceErr))
			continue
		}
		if err := ctx.Err(); err != nil {
			return errors.Join(err, files.remove(file.Name()))
		}
		if _, err := os.Lstat(destination); err == nil {
			return errors.Join(fmt.Errorf("download destination already exists: %s", destination), files.remove(file.Name()))
		} else if !os.IsNotExist(err) {
			return errors.Join(err, files.remove(file.Name()))
		}
		if err := files.rename(file.Name(), destination); err != nil {
			return errors.Join(err, files.remove(file.Name()))
		}
		return nil
	}
	return fmt.Errorf("asset %s: all download sources failed: %w", asset.Name, errors.Join(failures...))
}

// The two errors keep transport failures separate from failures writing local data.
func receiveReleaseAsset(ctx context.Context, client *http.Client, address string, asset releaseAsset, file downloadFile, progress *downloadProgress, idle time.Duration) (sourceErr, localErr error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	response, err := releaseGET(ctx, client, address)
	if err != nil {
		return err, nil
	}
	defer response.Body.Close()
	// Activity and expiry share the same lock; an expired timer cannot cancel a later source.
	var mu sync.Mutex
	last := time.Now()
	stopped := false
	var timer *time.Timer
	mu.Lock()
	timer = time.AfterFunc(idle, func() {
		mu.Lock()
		defer mu.Unlock()
		if stopped {
			return
		}
		remaining := idle - time.Since(last)
		if remaining > 0 {
			timer.Reset(remaining)
			return
		}
		stopped = true
		cancel()
	})
	mu.Unlock()
	defer func() { mu.Lock(); stopped = true; timer.Stop(); mu.Unlock() }()
	hash := sha256.New()
	reader := io.LimitReader(response.Body, asset.Size+1)
	buffer := make([]byte, 32*1024)
	var count int64
	for {
		n, readErr := reader.Read(buffer)
		if n > 0 {
			mu.Lock()
			last = time.Now()
			mu.Unlock()
		}
		count += int64(n)
		if count > asset.Size {
			return fmt.Errorf("size mismatch: response exceeds %d bytes", asset.Size), nil
		}
		if n > 0 {
			written, err := file.Write(buffer[:n])
			if err == nil && written != n {
				err = io.ErrShortWrite
			}
			if err != nil {
				return nil, err
			}
			_, _ = hash.Write(buffer[:n])
			_, _ = progress.Write(buffer[:n])
		}
		if readErr != nil {
			if readErr != io.EOF {
				return fmt.Errorf("response read: %w", readErr), nil
			}
			break
		}
		if err := ctx.Err(); err != nil {
			return err, nil
		}
	}
	progress.finish()
	if count != asset.Size {
		return fmt.Errorf("size mismatch: got %d, expected %d", count, asset.Size), nil
	}
	if hexDigest := fmt.Sprintf("%x", hash.Sum(nil)); hexDigest != asset.SHA256 {
		return fmt.Errorf("SHA256 mismatch"), nil
	}
	return nil, nil
}

var downloadErrorURL = regexp.MustCompile(`https?://[^\s<>"']+`)

func safeDownloadError(err error) error {
	if err == nil {
		return nil
	}
	return errors.New(downloadErrorURL.ReplaceAllStringFunc(err.Error(), func(raw string) string {
		address, parseErr := url.Parse(raw)
		if parseErr != nil {
			return "[URL]"
		}
		return address.Scheme + "://" + address.Host
	}))
}
