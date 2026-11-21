package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const videoIOServerRelease = "https://github.com/bluenviron/mediamtx/releases/download/v1.15.6/"

func videoIOAsset(platform string) (string, string) {
	switch platform {
	case "linux-x86_64":
		return "mediamtx_v1.15.6_linux_amd64.tar.gz", "3993d26c45b65cf337d5baf400e0e68e2f14689bcd31333ad34fa549f47ea55e"
	case "linux-aarch64":
		return "mediamtx_v1.15.6_linux_arm64.tar.gz", "09a51632e4457f830d69ecef4af6fb22596d26815468d0efd218dc4de571ad69"
	case "darwin-aarch64":
		return "mediamtx_v1.15.6_darwin_arm64.tar.gz", "caf8e49efa95dd3aeed8b4d3fb87153e315a293fdfaa072b0f632a8040b4660f"
	case "windows-x86_64":
		return "mediamtx_v1.15.6_windows_amd64.zip", "3e9895cfb38c50ab827aa0be3324e13b16b72d210d6c14e1e8dbb2e8fdb79cef"
	}
	return "", ""
}

func describeVideoIOStream(ctx context.Context, address string) error {
	dialer := net.Dialer{Timeout: time.Second}
	conn, err := dialer.DialContext(ctx, "tcp4", address)
	if err != nil {
		return err
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(time.Second)); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(conn, "DESCRIBE rtsp://%s/videoio RTSP/1.0\r\nCSeq: 1\r\nAccept: application/sdp\r\n\r\n", address); err != nil {
		return err
	}
	line, err := bufio.NewReader(io.LimitReader(conn, 4096)).ReadString('\n')
	if err != nil {
		return err
	}
	if !strings.HasPrefix(line, "RTSP/1.0 200 ") {
		return fmt.Errorf("RTSP DESCRIBE returned %s", strings.TrimSpace(line))
	}
	return nil
}

func startVideoIOFixture(ctx context.Context, o ciValidationOptions, evidence string, gstreamer bool) (string, string, func(), error) {
	dir, err := os.MkdirTemp(evidence, "videoio-")
	if err != nil {
		return "", "", nil, err
	}
	// 夹具由 testsrc2 生成（64x48、10 fps、4 秒），使用 libx264 ultrafast/zerolatency、GOP 10、yuv420p。
	fixture, err := os.ReadFile(filepath.Join(o.SourceRoot, "tests", "autolib", "fixtures", "rtsp-h264.ts"))
	if err != nil {
		return "", "", nil, err
	}
	if len(fixture) == 0 || len(fixture)%188 != 0 {
		return "", "", nil, fmt.Errorf("invalid videoio MPEG-TS fixture size %d", len(fixture))
	}
	executable, err := unpackVideoIOServer(ctx, o, dir)
	if err != nil {
		return "", "", nil, err
	}
	tcp, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return "", "", nil, err
	}
	address := tcp.Addr().String()
	var rtmpAddress string
	if gstreamer {
		rtmp, err := net.Listen("tcp4", "127.0.0.1:0")
		if err != nil {
			tcp.Close()
			return "", "", nil, err
		}
		rtmpAddress = rtmp.Addr().String()
		rtmp.Close()
	}
	tcp.Close()
	listen, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		return "", "", nil, err
	}
	udpAddress := listen.LocalAddr().(*net.UDPAddr)
	listen.Close()
	config := videoIOServerConfig(address, udpAddress.String(), rtmpAddress)
	configPath := filepath.Join(dir, "mediamtx.yml")
	if err := os.WriteFile(configPath, []byte(config), 0600); err != nil {
		return "", "", nil, err
	}
	sender, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		return "", "", nil, err
	}
	log, err := os.Create(filepath.Join(dir, "mediamtx.log"))
	if err != nil {
		sender.Close()
		return "", "", nil, err
	}
	fixtureCtx, cancel := context.WithCancel(ctx)
	server := ciExternalCommand(fixtureCtx, executable, configPath)
	server.Env = ciItemEnvironment(o.Environment)
	server.Stdout, server.Stderr = log, log
	if err := server.Start(); err != nil {
		cancel()
		log.Close()
		sender.Close()
		return "", "", nil, err
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer sender.Close()
		clock := time.NewTicker(35 * time.Millisecond)
		defer clock.Stop()
		for offset := 0; ; {
			select {
			case <-fixtureCtx.Done():
				return
			case <-clock.C:
			}
			end := min(offset+7*188, len(fixture))
			if _, err := sender.WriteToUDP(fixture[offset:end], udpAddress); err != nil {
				return
			}
			offset = end % len(fixture)
		}
	}()
	cleanup := func() {
		cancel()
		<-done
		_ = server.Wait()
		log.Close()
	}
	readyCtx, stopWaiting := context.WithTimeout(fixtureCtx, 15*time.Second)
	defer stopWaiting()
	retry := time.NewTicker(200 * time.Millisecond)
	defer retry.Stop()
	for {
		err = describeVideoIOStream(readyCtx, address)
		if err == nil {
			var rtmpURL string
			if gstreamer {
				rtmpURL = "rtmp://" + rtmpAddress + "/gstreamer-rtmp"
			}
			return "rtsp://" + address + "/videoio", rtmpURL, cleanup, nil
		}
		select {
		case <-readyCtx.Done():
			cleanup()
			return "", "", nil, fmt.Errorf("RTSP fixture not ready: %v (see %s)", err, log.Name())
		case <-retry.C:
		}
	}
}

func videoIOServerConfig(rtsp, udp, rtmp string) string {
	config := fmt.Sprintf("rtspAddress: %s\nrtspTransports: [tcp]\nhls: no\nwebrtc: no\nsrt: no\n", rtsp)
	if rtmp == "" {
		config += "rtmp: no\n"
	} else {
		config += fmt.Sprintf("rtmp: yes\nrtmpAddress: %s\n", rtmp)
	}
	config += fmt.Sprintf("paths:\n  videoio:\n    source: udp+mpegts://%s\n", udp)
	if rtmp != "" {
		config += "  gstreamer-rtsp:\n    source: publisher\n  gstreamer-rtmp:\n    source: publisher\n"
	}
	return config
}

func unpackVideoIOServer(ctx context.Context, o ciValidationOptions, dir string) (string, error) {
	name, expectedDigest := videoIOAsset(o.Context.OS + "-" + o.Context.Arch)
	if name == "" {
		return "", fmt.Errorf("no RTSP fixture for %s-%s", o.Context.OS, o.Context.Arch)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, videoIOServerRelease+name, nil)
	if err != nil {
		return "", err
	}
	client := http.Client{Timeout: time.Minute}
	response, err := client.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download RTSP fixture %s: %s", name, response.Status)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, 64<<20))
	if err != nil {
		return "", err
	}
	if ciSHA256(data) != expectedDigest {
		return "", fmt.Errorf("RTSP fixture SHA256 mismatch: %s", name)
	}
	archive := filepath.Join(dir, name)
	if err := os.WriteFile(archive, data, 0600); err != nil {
		return "", err
	}
	command := ciExternalCommand(ctx, o.CMakeCommand, "-E", "tar", "xf", archive)
	command.Env = ciItemEnvironment(o.Environment)
	command.Dir = dir
	if output, err := command.CombinedOutput(); err != nil {
		return "", fmt.Errorf("unpack RTSP fixture %s: %w\n%s", name, err, output)
	}
	if o.Context.OS == "windows" {
		return filepath.Join(dir, "mediamtx.exe"), nil
	}
	return filepath.Join(dir, "mediamtx"), nil
}
