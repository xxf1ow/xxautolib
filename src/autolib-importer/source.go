package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
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
	client := http.Client{Timeout: 5 * time.Minute}
	if configured != nil {
		client = *configured
		if client.Timeout == 0 {
			client.Timeout = 5 * time.Minute
		}
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

func downloadReleaseAsset(ctx context.Context, client *http.Client, asset releaseAsset, destination string, progress *downloadProgress) error {
	return downloadAsset(ctx, client, asset, destination, progress, downloadFiles{
		create: func(dir string) (downloadFile, error) { return os.CreateTemp(dir, ".download-") },
		remove: os.Remove, rename: os.Rename,
	})
}

func downloadAsset(ctx context.Context, client *http.Client, asset releaseAsset, destination string, progress *downloadProgress, files downloadFiles) error {
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
		sourceErr, localErr := receiveReleaseAsset(ctx, client, address, asset, file, progress)
		closeErr := file.Close()
		if sourceErr != nil || localErr != nil || closeErr != nil {
			removeErr := files.remove(file.Name())
			if err := errors.Join(localErr, closeErr, removeErr); err != nil {
				return err
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			host, _ := url.Parse(address)
			failures = append(failures, fmt.Errorf("source %d (%s): %w", index+1, host.Host, sourceErr))
			continue
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
func receiveReleaseAsset(ctx context.Context, client *http.Client, address string, asset releaseAsset, file downloadFile, progress *downloadProgress) (sourceErr, localErr error) {
	response, err := releaseGET(ctx, client, address)
	if err != nil {
		return err, nil
	}
	defer response.Body.Close()
	hash := sha256.New()
	progress.total, progress.downloaded, progress.reported = asset.Size, 0, 0
	progress.report()
	reader := io.LimitReader(response.Body, asset.Size+1)
	buffer := make([]byte, 32*1024)
	var count int64
	for {
		n, readErr := reader.Read(buffer)
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
