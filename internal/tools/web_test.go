package tools

import (
	"context"
	"net"
	"strings"
	"testing"
)

func TestIsPublicIP(t *testing.T) {
	cases := map[string]bool{
		"127.0.0.1":       false,
		"10.0.0.5":        false,
		"192.168.1.1":     false,
		"172.16.0.1":      false,
		"169.254.169.254": false, // 云元数据
		"100.64.0.1":      false, // CGNAT
		"0.0.0.0":         false,
		"::1":             false,
		"fd00::1":         false,
		"fe80::1":         false,
		"1.1.1.1":         true,
		"8.8.8.8":         true,
		"2606:4700::1111": true,
	}
	for ipStr, want := range cases {
		if got := isPublicIP(net.ParseIP(ipStr)); got != want {
			t.Errorf("isPublicIP(%s) = %v, want %v", ipStr, got, want)
		}
	}
}

func TestStripTags(t *testing.T) {
	in := `<b>标题</b>&amp;  说明 <script>x()</script>`
	if got := stripTags(in); !strings.Contains(got, "标题") || !strings.Contains(got, "& 说明") {
		t.Fatalf("stripTags = %q", got)
	}
}

func TestWebFetchRejectsInternal(t *testing.T) {
	tool := &webFetchTool{}
	out, err := tool.InvokableRun(context.Background(), `{"url":"http://127.0.0.1:8766/"}`, nil...)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "公网") && !strings.Contains(out, "error") {
		t.Fatalf("内网地址应被拒绝: %s", out)
	}
	// 非法 URL
	out2, _ := tool.InvokableRun(context.Background(), `{"url":"file:///etc/passwd"}`, nil...)
	if !strings.Contains(out2, "error") {
		t.Fatalf("非法协议应被拒绝: %s", out2)
	}
}

func TestCurrentTimeTool(t *testing.T) {
	out, err := (&currentTimeTool{}).InvokableRun(context.Background(), "", nil...)
	if err != nil || !strings.Contains(out, "当前时间") {
		t.Fatalf("out=%q err=%v", out, err)
	}
}
