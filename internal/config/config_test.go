package config_test

import (
	"os"
	"strings"
	"testing"

	"proxypools/internal/config"
)

func TestDefaultConfigIsValid(t *testing.T) {
	t.Setenv("ADMIN_PASSWORD_HASH", "hash")
	cfg := config.Default()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("expected default config to be valid, got %v", err)
	}
	if cfg.HTTPListenPort != 7777 {
		t.Fatalf("expected default HTTP port 7777, got %d", cfg.HTTPListenPort)
	}
	if cfg.SOCKSListenPort != 7780 {
		t.Fatalf("expected default SOCKS port 7780, got %d", cfg.SOCKSListenPort)
	}
	if cfg.Dispatcher.Algorithm != "sequential" {
		t.Fatalf("expected default dispatcher algorithm sequential, got %s", cfg.Dispatcher.Algorithm)
	}
	ports := cfg.ResolvedPorts()
	if len(ports) != 1 {
		t.Fatalf("expected one resolved default port, got %d", len(ports))
	}
	if ports[0].Key != config.DefaultPortKey {
		t.Fatalf("expected default port key, got %s", ports[0].Key)
	}
	if len(ports[0].Lanes) != 2 {
		t.Fatalf("expected default lanes to be synthesized, got %#v", ports[0].Lanes)
	}
}

func TestDefaultReadsEnvironmentOverrides(t *testing.T) {
	t.Setenv("ADMIN_USERNAME", "root")
	t.Setenv("ADMIN_PASSWORD_HASH", "hash")
	t.Setenv("ADMIN_LISTEN_PORT", "18080")
	t.Setenv("HTTP_LISTEN_PORT", "17777")
	t.Setenv("RUNTIME_MODE", "pool")
	t.Setenv("POOL_ALGORITHM", "random")
	t.Setenv("DISPATCHER_ENABLED", "true")
	t.Setenv("DISPATCHER_HTTP_LISTEN_PORT", "28080")
	t.Setenv("DISPATCHER_ALGORITHM", "balance")
	cfg := config.Default()
	if cfg.AdminUsername != "root" {
		t.Fatalf("expected admin username root, got %s", cfg.AdminUsername)
	}
	if cfg.AdminPasswordHash != "hash" {
		t.Fatalf("expected password hash from env, got %s", cfg.AdminPasswordHash)
	}
	if cfg.AdminListenPort != 18080 {
		t.Fatalf("expected admin port 18080, got %d", cfg.AdminListenPort)
	}
	if cfg.HTTPListenPort != 17777 {
		t.Fatalf("expected http port 17777, got %d", cfg.HTTPListenPort)
	}
	if cfg.RuntimeMode != "pool" {
		t.Fatalf("expected runtime mode pool, got %s", cfg.RuntimeMode)
	}
	if cfg.PoolAlgorithm != "random" {
		t.Fatalf("expected pool algorithm random, got %s", cfg.PoolAlgorithm)
	}
	if !cfg.Dispatcher.Enabled {
		t.Fatal("expected dispatcher enabled from env")
	}
	if cfg.Dispatcher.HTTPListenPort != 28080 {
		t.Fatalf("expected dispatcher http port 28080, got %d", cfg.Dispatcher.HTTPListenPort)
	}
	if cfg.Dispatcher.Algorithm != "balance" {
		t.Fatalf("expected dispatcher algorithm balance, got %s", cfg.Dispatcher.Algorithm)
	}
}

func TestDefaultReadsPortsJSONWithExplicitLanes(t *testing.T) {
	t.Setenv("ADMIN_PASSWORD_HASH", "hash")
	t.Setenv("PORTS_JSON", `[
		{"key":"default","name":"默认入口","http_listen_port":7777,"socks_listen_port":7780,
		 "lanes":[
		   {"key":"lane-http-a","protocol":"http","listen_addr":"127.0.0.1","listen_port":17001},
		   {"key":"lane-socks-a","protocol":"socks","listen_addr":"127.0.0.1","listen_port":17002}
		 ]},
		{"key":"canary","name":"灰度入口","http_listen_port":8777,"socks_listen_port":8780,"runtime_mode":"pool","pool_algorithm":"balance"}
	]`)
	cfg := config.Default()
	if len(cfg.Ports) != 2 {
		t.Fatalf("expected 2 explicit ports, got %d", len(cfg.Ports))
	}
	ports := cfg.ResolvedPorts()
	if ports[0].Lanes[0].Key != "lane-http-a" {
		t.Fatalf("expected explicit lane to be preserved, got %#v", ports[0].Lanes)
	}
	if ports[1].RuntimeMode != "pool" || ports[1].PoolAlgorithm != "balance" {
		t.Fatalf("expected explicit runtime settings on canary, got %#v", ports[1])
	}
}

func TestValidateRequiresAdminPasswordHash(t *testing.T) {
	_ = os.Unsetenv("ADMIN_PASSWORD_HASH")
	cfg := config.Default()
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected missing admin password hash to fail validation")
	}
}

func TestValidateRejectsPortsWithoutDefault(t *testing.T) {
	t.Setenv("ADMIN_PASSWORD_HASH", "hash")
	cfg := config.Default()
	cfg.Ports = []config.PortConfig{{
		Key:             "canary",
		HTTPListenAddr:  "0.0.0.0",
		HTTPListenPort:  8777,
		SOCKSListenAddr: "0.0.0.0",
		SOCKSListenPort: 8780,
	}}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected ports without default to fail validation")
	}
}

func TestValidateRejectsPortBindingConflict(t *testing.T) {
	t.Setenv("ADMIN_PASSWORD_HASH", "hash")
	cfg := config.Default()
	cfg.Ports = []config.PortConfig{
		{Key: "default"},
		{Key: "canary", HTTPListenAddr: cfg.HTTPListenAddr, HTTPListenPort: cfg.HTTPListenPort, SOCKSListenAddr: cfg.SOCKSListenAddr, SOCKSListenPort: 8780},
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected conflicting port binding to fail validation")
	}
}

func TestValidateRejectsDispatcherBindingConflict(t *testing.T) {
	t.Setenv("ADMIN_PASSWORD_HASH", "hash")
	cfg := config.Default()
	cfg.Dispatcher.Enabled = true
	cfg.Dispatcher.HTTPListenAddr = cfg.DefaultPort().HTTPListenAddr
	cfg.Dispatcher.HTTPListenPort = cfg.DefaultPort().HTTPListenPort
	cfg.Dispatcher.SOCKSListenAddr = "127.0.0.1"
	cfg.Dispatcher.SOCKSListenPort = 27880
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected dispatcher binding conflict to fail validation")
	}
}

func TestValidateRejectsLaneBindingConflict(t *testing.T) {
	t.Setenv("ADMIN_PASSWORD_HASH", "hash")
	cfg := config.Default()
	cfg.Ports = []config.PortConfig{{
		Key:             "default",
		HTTPListenAddr:  "127.0.0.1",
		HTTPListenPort:  7777,
		SOCKSListenAddr: "127.0.0.1",
		SOCKSListenPort: 7780,
		Lanes: []config.LaneConfig{
			{Key: "lane-http-a", Protocol: "http", ListenAddr: "127.0.0.1", ListenPort: 17001},
			{Key: "lane-http-b", Protocol: "http", ListenAddr: "127.0.0.1", ListenPort: 17001},
		},
	}}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected lane binding conflict to fail validation")
	}
}

func TestValidateRejectsDispatcherRuleWithoutTargetLane(t *testing.T) {
	t.Setenv("ADMIN_PASSWORD_HASH", "hash")
	cfg := config.Default()
	cfg.Dispatcher.Enabled = true
	cfg.Dispatcher.HTTPListenAddr = "127.0.0.1"
	cfg.Dispatcher.HTTPListenPort = 27881
	cfg.Dispatcher.SOCKSListenAddr = "127.0.0.1"
	cfg.Dispatcher.SOCKSListenPort = 27882
	cfg.Dispatcher.Rules = []config.DispatcherRuleConfig{{
		Name:          "host-route",
		Host:          "api.example.com",
		TargetPortKey: config.DefaultPortKey,
	}}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "target_lane_key") {
		t.Fatalf("expected target_lane_key validation error, got %v", err)
	}
}

func TestValidateRejectsDispatcherRuleHeaderValueWithoutName(t *testing.T) {
	t.Setenv("ADMIN_PASSWORD_HASH", "hash")
	cfg := config.Default()
	cfg.Dispatcher.Enabled = true
	cfg.Dispatcher.HTTPListenAddr = "127.0.0.1"
	cfg.Dispatcher.HTTPListenPort = 27883
	cfg.Dispatcher.SOCKSListenAddr = "127.0.0.1"
	cfg.Dispatcher.SOCKSListenPort = 27884
	cfg.Dispatcher.Rules = []config.DispatcherRuleConfig{{
		Name:          "header-route",
		HeaderValue:   "blue",
		TargetPortKey: config.DefaultPortKey,
		TargetLaneKey: "lane-http-1",
	}}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "header_value requires header_name") {
		t.Fatalf("expected header_value/header_name validation error, got %v", err)
	}
}

func TestValidateRejectsDispatcherRuleHeaderNameWithoutValue(t *testing.T) {
	t.Setenv("ADMIN_PASSWORD_HASH", "hash")
	cfg := config.Default()
	cfg.Dispatcher.Enabled = true
	cfg.Dispatcher.HTTPListenAddr = "127.0.0.1"
	cfg.Dispatcher.HTTPListenPort = 27885
	cfg.Dispatcher.SOCKSListenAddr = "127.0.0.1"
	cfg.Dispatcher.SOCKSListenPort = 27886
	cfg.Dispatcher.Rules = []config.DispatcherRuleConfig{{
		Name:          "header-route",
		HeaderName:    "X-Tenant",
		TargetPortKey: config.DefaultPortKey,
		TargetLaneKey: "lane-http-1",
	}}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "header_value is required") {
		t.Fatalf("expected missing header_value validation error, got %v", err)
	}
}
