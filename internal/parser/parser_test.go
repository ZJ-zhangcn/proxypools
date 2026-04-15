package parser_test

import (
	"strings"
	"testing"

	"proxypools/internal/parser"
)

func TestParseClashSubscription(t *testing.T) {
	input := []byte("proxies:\n  - name: hk-1\n    type: vmess\n    server: hk.example.com\n    port: 443\n    uuid: 11111111-1111-1111-1111-111111111111\n")
	nodes, err := parser.ParseSubscription(input)
	if err != nil {
		t.Fatalf("parse clash failed: %v", err)
	}
	if len(nodes) != 1 {
		t.Fatalf("expected 1 node, got %d", len(nodes))
	}
	if nodes[0].Name != "hk-1" {
		t.Fatalf("expected node name hk-1, got %s", nodes[0].Name)
	}
	if nodes[0].ProtocolType != "vmess" {
		t.Fatalf("expected vmess, got %s", nodes[0].ProtocolType)
	}
}

func TestParseShareLinks(t *testing.T) {
	input := []byte("ss://YWVzLTI1Ni1nY206cGFzc0BleGFtcGxlLmNvbTo4Mzg4#jp-1\n")
	nodes, err := parser.ParseSubscription(input)
	if err != nil {
		t.Fatalf("parse share link failed: %v", err)
	}
	if len(nodes) != 1 {
		t.Fatalf("expected 1 node, got %d", len(nodes))
	}
	if nodes[0].ProtocolType != "shadowsocks" {
		t.Fatalf("expected shadowsocks, got %s", nodes[0].ProtocolType)
	}
	if nodes[0].Name != "jp-1" {
		t.Fatalf("expected node name jp-1, got %s", nodes[0].Name)
	}
}

func TestParseSubscriptionSkipsUnsupportedShareLinks(t *testing.T) {
	input := []byte("tuic://secret@example.com:443#unsupported\nss://YWVzLTI1Ni1nY206cGFzc0BleGFtcGxlLmNvbTo4Mzg4#jp-1\n")
	nodes, err := parser.ParseSubscription(input)
	if err != nil {
		t.Fatalf("expected unsupported share link to be skipped, got %v", err)
	}
	if len(nodes) != 1 || nodes[0].ProtocolType != "shadowsocks" {
		t.Fatalf("expected supported node to remain, got %#v", nodes)
	}
}

func TestParseSupportedMalformedLinkStillFails(t *testing.T) {
	input := []byte("trojan://password@example.com?sni=example.com#bad")
	_, err := parser.ParseSubscription(input)
	if err == nil {
		t.Fatal("expected malformed supported trojan link to fail")
	}
}

func TestParseBase64EncodedSubscription(t *testing.T) {
	input := []byte("c3M6Ly9ZV1Z6TFRJMU5pMW5ZMjA2Y0dGemMwQmxlR0Z0Y0d4bExtTnZiVG80TXpnNCMqc3MtMQ==")
	nodes, err := parser.ParseSubscription(input)
	if err != nil {
		t.Fatalf("parse base64 subscription failed: %v", err)
	}
	if len(nodes) != 1 {
		t.Fatalf("expected 1 node, got %d", len(nodes))
	}
	if nodes[0].ProtocolType != "shadowsocks" {
		t.Fatalf("expected shadowsocks, got %s", nodes[0].ProtocolType)
	}
}

func TestParseTrojanLink(t *testing.T) {
	input := []byte("trojan://password@example.com:443?sni=example.com&allowInsecure=1&type=tcp#trojan-node")
	nodes, err := parser.ParseSubscription(input)
	if err != nil {
		t.Fatalf("parse trojan failed: %v", err)
	}
	if len(nodes) != 1 || nodes[0].ProtocolType != "trojan" {
		t.Fatalf("expected one trojan node, got %#v", nodes)
	}
}

func TestParseVLESSLink(t *testing.T) {
	input := []byte("vless://11111111-1111-1111-1111-111111111111@example.com:443?security=reality&sni=www.example.com&pbk=pubkey&sid=abcd&type=tcp#vless-node")
	nodes, err := parser.ParseSubscription(input)
	if err != nil {
		t.Fatalf("parse vless failed: %v", err)
	}
	if len(nodes) != 1 || nodes[0].ProtocolType != "vless" {
		t.Fatalf("expected one vless node, got %#v", nodes)
	}
}

func TestParseHysteria2Link(t *testing.T) {
	input := []byte("hysteria2://password@example.com:8443?insecure=1&sni=example.com&mport=60000-60010#hy2-node")
	nodes, err := parser.ParseSubscription(input)
	if err != nil {
		t.Fatalf("parse hysteria2 failed: %v", err)
	}
	if len(nodes) != 1 || nodes[0].ProtocolType != "hysteria2" {
		t.Fatalf("expected one hysteria2 node, got %#v", nodes)
	}
}

func TestParseAnyTLSLink(t *testing.T) {
	input := []byte("anytls://secret@example.com:443?security=tls&type=tcp&packetEncoding=none&alpn=h2&allowInsecure=1&sni=sg01.mozilla.org&fp=chrome&udp=1&insecure=1#anytls-node")
	nodes, err := parser.ParseSubscription(input)
	if err != nil {
		t.Fatalf("parse anytls failed: %v", err)
	}
	if len(nodes) != 1 || nodes[0].ProtocolType != "anytls" {
		t.Fatalf("expected one anytls node, got %#v", nodes)
	}
	payload := nodes[0].PayloadJSON
	for _, snippet := range []string{
		`"type":"anytls"`,
		`"server":"example.com"`,
		`"server_port":443`,
		`"password":"secret"`,
		`"server_name":"sg01.mozilla.org"`,
		`"insecure":true`,
		`"fingerprint":"chrome"`,
		`"alpn":["h2"]`,
	} {
		if !strings.Contains(payload, snippet) {
			t.Fatalf("expected payload to contain %s, got %s", snippet, payload)
		}
	}
}

func TestParseAnyTLSBase64Subscription(t *testing.T) {
	input := []byte("YW55dGxzOi8vc2VjcmV0QGV4YW1wbGUuY29tOjQ0Mz9zZWN1cml0eT10bHMmdHlwZT10Y3AmcGFja2V0RW5jb2Rpbmc9bm9uZSZhbHBuPWgyI3Rlc3Q=")
	nodes, err := parser.ParseSubscription(input)
	if err != nil {
		t.Fatalf("parse anytls base64 subscription failed: %v", err)
	}
	if len(nodes) != 1 || nodes[0].ProtocolType != "anytls" {
		t.Fatalf("expected one anytls node, got %#v", nodes)
	}
}

func TestParseAnyTLSRejectsUnsupportedTransport(t *testing.T) {
	input := []byte("anytls://secret@example.com:443?type=ws#bad")
	_, err := parser.ParseSubscription(input)
	if err == nil || !strings.Contains(err.Error(), "unsupported anytls transport") {
		t.Fatalf("expected unsupported anytls transport error, got %v", err)
	}
}

func TestParseAnyTLSRejectsUnsupportedSecurity(t *testing.T) {
	input := []byte("anytls://secret@example.com:443?security=reality#bad")
	_, err := parser.ParseSubscription(input)
	if err == nil || !strings.Contains(err.Error(), "unsupported anytls security") {
		t.Fatalf("expected unsupported anytls security error, got %v", err)
	}
}

func TestParseAnyTLSRejectsUnsupportedPacketEncoding(t *testing.T) {
	input := []byte("anytls://secret@example.com:443?packetEncoding=xudp#bad")
	_, err := parser.ParseSubscription(input)
	if err == nil || !strings.Contains(err.Error(), "unsupported anytls packetEncoding") {
		t.Fatalf("expected unsupported anytls packetEncoding error, got %v", err)
	}
}

func TestParseAnyTLSRejectsEmptyPassword(t *testing.T) {
	input := []byte("anytls://example.com:443#bad")
	_, err := parser.ParseSubscription(input)
	if err == nil || !strings.Contains(err.Error(), "invalid anytls password") {
		t.Fatalf("expected invalid anytls password error, got %v", err)
	}
}

func TestParseClashAnyTLSProxy(t *testing.T) {
	input := []byte("proxies:\n  - name: anytls-1\n    type: anytls\n    server: example.com\n    port: 443\n    password: secret\n    sni: sg01.mozilla.org\n    skip-cert-verify: true\n    client-fingerprint: chrome\n    alpn:\n      - h2\n    idle-session-check-interval: 30s\n    idle-session-timeout: 5m\n    min-idle-session: 2\n")
	nodes, err := parser.ParseSubscription(input)
	if err != nil {
		t.Fatalf("parse clash anytls failed: %v", err)
	}
	if len(nodes) != 1 || nodes[0].ProtocolType != "anytls" {
		t.Fatalf("expected one anytls clash node, got %#v", nodes)
	}
	payload := nodes[0].PayloadJSON
	for _, snippet := range []string{
		`"type":"anytls"`,
		`"password":"secret"`,
		`"server_name":"sg01.mozilla.org"`,
		`"insecure":true`,
		`"fingerprint":"chrome"`,
		`"alpn":["h2"]`,
		`"idle_session_check_interval":"30s"`,
		`"idle_session_timeout":"5m"`,
		`"min_idle_session":2`,
	} {
		if !strings.Contains(payload, snippet) {
			t.Fatalf("expected payload to contain %s, got %s", snippet, payload)
		}
	}
}

func TestParseClashAnyTLSProxyWithServerNameKeepsTLSOptions(t *testing.T) {
	input := []byte("proxies:\n  - name: anytls-2\n    type: anytls\n    server: example.com\n    port: 443\n    password: secret\n    servername: sg01.mozilla.org\n    tls: true\n    skip-cert-verify: true\n    client-fingerprint: chrome\n    alpn:\n      - h2\n")
	nodes, err := parser.ParseSubscription(input)
	if err != nil {
		t.Fatalf("parse clash anytls with servername failed: %v", err)
	}
	if len(nodes) != 1 || nodes[0].ProtocolType != "anytls" {
		t.Fatalf("expected one anytls clash node, got %#v", nodes)
	}
	payload := nodes[0].PayloadJSON
	for _, snippet := range []string{
		`"server_name":"sg01.mozilla.org"`,
		`"insecure":true`,
		`"fingerprint":"chrome"`,
		`"alpn":["h2"]`,
	} {
		if !strings.Contains(payload, snippet) {
			t.Fatalf("expected payload to contain %s, got %s", snippet, payload)
		}
	}
}

func TestParseClashAnyTLSRejectsEmptyPassword(t *testing.T) {
	input := []byte("proxies:\n  - name: anytls-bad\n    type: anytls\n    server: example.com\n    port: 443\n")
	_, err := parser.ParseSubscription(input)
	if err == nil || !strings.Contains(err.Error(), "invalid anytls clash proxy payload") {
		t.Fatalf("expected invalid anytls clash proxy payload error, got %v", err)
	}
}

func TestParseClashAnyTLSRejectsUnsupportedNetwork(t *testing.T) {
	input := []byte("proxies:\n  - name: anytls-bad\n    type: anytls\n    server: example.com\n    port: 443\n    password: secret\n    network: ws\n")
	_, err := parser.ParseSubscription(input)
	if err == nil || !strings.Contains(err.Error(), "unsupported anytls clash network") {
		t.Fatalf("expected unsupported anytls clash network error, got %v", err)
	}
}
