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
