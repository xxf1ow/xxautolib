package main

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"
)

func TestVideoIOFixture(t *testing.T) {
	const rtsp = "127.0.0.1:8554"
	const udp = "127.0.0.1:8054"
	const rtmp = "127.0.0.1:1935"
	if config := videoIOServerConfig(rtsp, udp, ""); !strings.Contains(config, "rtmp: no\n") || strings.Contains(config, "source: publisher") {
		t.Fatalf("OpenCV fixture changed its publisher policy:\n%s", config)
	}
	if config := videoIOServerConfig(rtsp, udp, rtmp); !strings.Contains(config, "rtmpAddress: "+rtmp+"\n") ||
		!strings.Contains(config, "gstreamer-rtsp:\n    source: publisher\n") ||
		!strings.Contains(config, "gstreamer-rtmp:\n    source: publisher\n") {
		t.Fatalf("GStreamer fixture does not expose separate publish paths:\n%s", config)
	}
	for _, status := range []int{200, 404} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			listener, err := net.Listen("tcp4", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			request := make(chan string, 1)
			go func() {
				conn, err := listener.Accept()
				if err != nil {
					return
				}
				defer conn.Close()
				line, _ := bufio.NewReader(conn).ReadString('\n')
				request <- line
				fmt.Fprintf(conn, "RTSP/1.0 %d Status\r\nCSeq: 1\r\n\r\n", status)
			}()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			address := listener.Addr().String()
			err = describeVideoIOStream(ctx, address)
			if (err == nil) != (status == 200) {
				t.Fatalf("RTSP status %d: %v", status, err)
			}
			select {
			case line := <-request:
				if !strings.HasPrefix(line, "DESCRIBE rtsp://"+address+"/videoio RTSP/1.0") {
					t.Fatal(line)
				}
			case <-ctx.Done():
				t.Fatal("RTSP probe did not send DESCRIBE")
			}
		})
	}
	for _, platform := range []string{"darwin-aarch64", "linux-x86_64", "linux-aarch64", "windows-x86_64"} {
		name, digest := videoIOAsset(platform)
		if !strings.HasPrefix(name, "mediamtx_v1.15.6_") || !digestValue.MatchString(digest) {
			t.Fatalf("invalid pinned MediaMTX asset for %s: %s %s", platform, name, digest)
		}
	}
}
