package parser_test

import (
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
