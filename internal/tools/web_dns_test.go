package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"golang.org/x/net/dns/dnsmessage"
)

func TestWebFetchRejectsDNSRebinding(t *testing.T) {
	dns, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer dns.Close()
	var queries atomic.Int32
	go func() {
		buf := make([]byte, 2048)
		for {
			n, peer, err := dns.ReadFrom(buf)
			if err != nil {
				return
			}
			var msg dnsmessage.Message
			if msg.Unpack(buf[:n]) != nil {
				continue
			}
			msg.Header.Response = true
			msg.Header.Authoritative = true
			msg.Answers = nil
			for _, q := range msg.Questions {
				if q.Type == dnsmessage.TypeA {
					ip := [4]byte{8, 8, 8, 8}
					if queries.Add(1) > 1 {
						ip = [4]byte{127, 0, 0, 1}
					}
					msg.Answers = append(msg.Answers, dnsmessage.Resource{
						Header: dnsmessage.ResourceHeader{Name: q.Name, Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET},
						Body:   &dnsmessage.AResource{A: ip},
					})
				}
			}
			payload, err := msg.Pack()
			if err == nil {
				_, _ = dns.WriteTo(payload, peer)
			}
		}
	}()
	// 不并行运行：本测试替换进程默认解析器以模拟两次解析间的地址变化。
	oldResolver := net.DefaultResolver
	net.DefaultResolver = &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "udp", dns.LocalAddr().String())
	}}
	defer func() { net.DefaultResolver = oldResolver }()
	var requests atomic.Int32
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		fmt.Fprint(w, "private-server-sentinel")
	}))
	defer local.Close()
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(local.URL, "http://"))
	args, _ := json.Marshal(map[string]string{"url": "http://review-rebind.test:" + port})
	out, err := (&webFetchTool{}).InvokableRun(context.Background(), string(args))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "公网") || strings.Contains(out, "private-server-sentinel") || requests.Load() != 0 {
		t.Fatalf("rebind reached private server: %s requests=%d", out, requests.Load())
	}
	if queries.Load() < 2 {
		t.Fatal("did not exercise connection-time DNS resolution")
	}
}

func TestWebFetchTransportDoesNotUseEnvironmentProxy(t *testing.T) {
	if webFetchTransport.Proxy != nil {
		t.Fatal("environment proxy could bypass public-IP dialer")
	}
}
