package runtime_test

import (
	"strings"
	"testing"

	"proxypools/internal/model"
	"proxypools/internal/runtime"
)

func TestBuildConfigIncludesFixedInboundsAndSelectors(t *testing.T) {
	nodes := []model.Node{{
		ID:           1,
		Name:         "hk-1",
		ProtocolType: "vmess",
		Server:       "hk.example.com",
		Port:         443,
		PayloadJSON:  `{"uuid":"11111111-1111-1111-1111-111111111111","type":"vmess","server":"hk.example.com","server_port":443}`,
	}}

	cfg, err := runtime.BuildConfig(runtime.BuildInput{
		HTTPListenAddr:   "0.0.0.0",
		HTTPPort:         7777,
		SOCKSListenAddr:  "0.0.0.0",
		SOCKSPort:        7780,
		HealthListenAddr: "127.0.0.1",
		HealthPort:       19090,
		Nodes:            nodes,
		ActiveNodeID:     1,
	})
	if err != nil {
		t.Fatalf("build config failed: %v", err)
	}
	if !strings.Contains(cfg, `"tag":"http-in"`) {
		t.Fatal("expected http-in inbound")
	}
	if !strings.Contains(cfg, `"tag":"active-http"`) {
		t.Fatal("expected active-http selector")
	}
	if !strings.Contains(cfg, `"tag":"health-check"`) {
		t.Fatal("expected health-check selector")
	}
	if !strings.Contains(cfg, `"tag":"node-1"`) {
		t.Fatal("expected node outbound tag")
	}
}

func TestBuildConfigGeneratesPortScopedInboundsSelectorsAndLanes(t *testing.T) {
	nodes := []model.Node{{
		ID:           1,
		Name:         "hk-1",
		ProtocolType: "vmess",
		Server:       "hk.example.com",
		Port:         443,
		PayloadJSON:  `{"uuid":"11111111-1111-1111-1111-111111111111","type":"vmess","server":"hk.example.com","server_port":443}`,
	}, {
		ID:           2,
		Name:         "jp-1",
		ProtocolType: "vmess",
		Server:       "jp.example.com",
		Port:         443,
		PayloadJSON:  `{"uuid":"22222222-2222-2222-2222-222222222222","type":"vmess","server":"jp.example.com","server_port":443}`,
	}}

	cfg, err := runtime.BuildConfig(runtime.BuildInput{
		HealthListenAddr: "127.0.0.1",
		HealthPort:       19090,
		Nodes:            nodes,
		ActiveNodeID:     1,
		Ports: []runtime.PortBuildInput{
			{Key: "default", HTTPListenAddr: "0.0.0.0", HTTPPort: 7777, SOCKSListenAddr: "0.0.0.0", SOCKSPort: 7780, ActiveNodeID: 1, Lanes: []runtime.LaneBuildInput{{Key: "lane-http-1", Protocol: "http", ListenAddr: "127.0.0.1", ListenPort: 17001, ActiveNodeID: 1}, {Key: "lane-socks-1", Protocol: "socks", ListenAddr: "127.0.0.1", ListenPort: 17002, ActiveNodeID: 1}}},
			{Key: "canary", HTTPListenAddr: "127.0.0.1", HTTPPort: 8777, SOCKSListenAddr: "127.0.0.1", SOCKSPort: 8780, ActiveNodeID: 2, Lanes: []runtime.LaneBuildInput{{Key: "lane-http-a", Protocol: "http", ListenAddr: "127.0.0.1", ListenPort: 18001, ActiveNodeID: 2}, {Key: "lane-socks-a", Protocol: "socks", ListenAddr: "127.0.0.1", ListenPort: 18002, ActiveNodeID: 1}}},
		},
	})
	if err != nil {
		t.Fatalf("build config failed: %v", err)
	}
	if !strings.Contains(cfg, `"tag":"http-in-canary"`) {
		t.Fatal("expected canary http inbound")
	}
	if !strings.Contains(cfg, `"tag":"active-http-canary"`) {
		t.Fatal("expected canary active-http selector")
	}
	if !strings.Contains(cfg, `"tag":"http-in-canary-lane-http-a"`) {
		t.Fatal("expected canary lane http inbound")
	}
	if !strings.Contains(cfg, `"tag":"active-http-canary-lane-http-a"`) {
		t.Fatal("expected canary lane http selector")
	}
	if !strings.Contains(cfg, `"tag":"socks-in-canary-lane-socks-a"`) {
		t.Fatal("expected canary lane socks inbound")
	}
	if !strings.Contains(cfg, `"listen_port":18001`) {
		t.Fatal("expected canary lane listen port in config")
	}
	if !strings.Contains(cfg, `"default":"node-2"`) {
		t.Fatal("expected canary selector or lane to point at node-2")
	}
	if !strings.Contains(cfg, `"inbound":["http-in-canary-lane-http-a"],"outbound":"active-http-canary-lane-http-a"`) {
		t.Fatal("expected canary lane route rule")
	}
}

func TestBuildConfigPassesThroughAnyTLSOutbound(t *testing.T) {
	nodes := []model.Node{{
		ID:           1,
		Name:         "anytls-1",
		ProtocolType: "anytls",
		Server:       "example.com",
		Port:         443,
		PayloadJSON:  `{"type":"anytls","server":"example.com","server_port":443,"password":"secret","tls":{"enabled":true,"server_name":"sg01.mozilla.org","insecure":true,"utls":{"enabled":true,"fingerprint":"chrome"},"alpn":["h2"]}}`,
	}}

	cfg, err := runtime.BuildConfig(runtime.BuildInput{
		HTTPListenAddr:   "0.0.0.0",
		HTTPPort:         7777,
		SOCKSListenAddr:  "0.0.0.0",
		SOCKSPort:        7780,
		HealthListenAddr: "127.0.0.1",
		HealthPort:       19090,
		Nodes:            nodes,
		ActiveNodeID:     1,
	})
	if err != nil {
		t.Fatalf("build config failed: %v", err)
	}
	for _, snippet := range []string{
		`"type":"anytls"`,
		`"tag":"node-1"`,
		`"server":"example.com"`,
		`"server_name":"sg01.mozilla.org"`,
		`"fingerprint":"chrome"`,
		`"alpn":["h2"]`,
	} {
		if !strings.Contains(cfg, snippet) {
			t.Fatalf("expected config to contain %s, got %s", snippet, cfg)
		}
	}
}
