package config

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
)

const DefaultPortKey = "default"

var portKeyPattern = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

type LaneConfig struct {
	Key        string `json:"key"`
	Protocol   string `json:"protocol"`
	ListenAddr string `json:"listen_addr"`
	ListenPort int    `json:"listen_port"`
	Weight     int    `json:"weight"`
}

type PortConfig struct {
	Key             string       `json:"key"`
	Name            string       `json:"name"`
	HTTPListenAddr  string       `json:"http_listen_addr"`
	HTTPListenPort  int          `json:"http_listen_port"`
	SOCKSListenAddr string       `json:"socks_listen_addr"`
	SOCKSListenPort int          `json:"socks_listen_port"`
	RuntimeMode     string       `json:"runtime_mode"`
	PoolAlgorithm   string       `json:"pool_algorithm"`
	Lanes           []LaneConfig `json:"lanes"`
}

type DispatcherRuleConfig struct {
	Name          string `json:"name"`
	Host          string `json:"host"`
	HeaderName    string `json:"header_name"`
	HeaderValue   string `json:"header_value"`
	TargetPortKey string `json:"target_port_key"`
	TargetLaneKey string `json:"target_lane_key"`
}

type DispatcherConfig struct {
	Enabled         bool
	HTTPListenAddr  string
	HTTPListenPort  int
	SOCKSListenAddr string
	SOCKSListenPort int
	Algorithm       string
	Rules           []DispatcherRuleConfig
}

type Config struct {
	AdminListenAddr             string
	AdminListenPort             int
	HTTPListenAddr              string
	HTTPListenPort              int
	SOCKSListenAddr             string
	SOCKSListenPort             int
	HealthListenAddr            string
	HealthListenPort            int
	DBPath                      string
	SingboxBinary               string
	SingboxConfigPath           string
	SubscriptionRefreshInterval int
	HealthCheckInterval         int
	SubscriptionURL             string
	AdminUsername               string
	AdminPasswordHash           string
	RuntimeMode                 string
	PoolAlgorithm               string
	Dispatcher                  DispatcherConfig
	Ports                       []PortConfig

	portsParseError error
}

func Default() Config {
	cfg := Config{
		AdminListenAddr:             "0.0.0.0",
		AdminListenPort:             8080,
		HTTPListenAddr:              "0.0.0.0",
		HTTPListenPort:              7777,
		SOCKSListenAddr:             "0.0.0.0",
		SOCKSListenPort:             7780,
		HealthListenAddr:            "127.0.0.1",
		HealthListenPort:            19090,
		DBPath:                      "data/proxypools.db",
		SingboxBinary:               "sing-box",
		SingboxConfigPath:           "data/sing-box.json",
		SubscriptionRefreshInterval: 900,
		HealthCheckInterval:         60,
		AdminUsername:               "admin",
		RuntimeMode:                 "single_active",
		PoolAlgorithm:               "sequential",
		Dispatcher: DispatcherConfig{
			Enabled:         false,
			HTTPListenAddr:  "0.0.0.0",
			HTTPListenPort:  7777,
			SOCKSListenAddr: "0.0.0.0",
			SOCKSListenPort: 7780,
			Algorithm:       "sequential",
		},
	}

	if v := os.Getenv("ADMIN_USERNAME"); v != "" {
		cfg.AdminUsername = v
	}
	if v := os.Getenv("ADMIN_PASSWORD_HASH"); v != "" {
		cfg.AdminPasswordHash = v
	}
	if v := os.Getenv("SUBSCRIPTION_URL"); v != "" {
		cfg.SubscriptionURL = v
	}
	if v := os.Getenv("ADMIN_LISTEN_ADDR"); v != "" {
		cfg.AdminListenAddr = v
	}
	if v := os.Getenv("HTTP_LISTEN_ADDR"); v != "" {
		cfg.HTTPListenAddr = v
	}
	if v := os.Getenv("SOCKS_LISTEN_ADDR"); v != "" {
		cfg.SOCKSListenAddr = v
	}
	if v := os.Getenv("HEALTH_LISTEN_ADDR"); v != "" {
		cfg.HealthListenAddr = v
	}
	if v := os.Getenv("SINGBOX_BINARY"); v != "" {
		cfg.SingboxBinary = v
	}
	if v := os.Getenv("SINGBOX_CONFIG_PATH"); v != "" {
		cfg.SingboxConfigPath = v
	}
	if v := os.Getenv("DB_PATH"); v != "" {
		cfg.DBPath = v
	}
	if v := os.Getenv("ADMIN_LISTEN_PORT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.AdminListenPort = n
		}
	}
	if v := os.Getenv("HTTP_LISTEN_PORT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.HTTPListenPort = n
		}
	}
	if v := os.Getenv("SOCKS_LISTEN_PORT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.SOCKSListenPort = n
		}
	}
	if v := os.Getenv("HEALTH_LISTEN_PORT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.HealthListenPort = n
		}
	}
	if v := os.Getenv("SUBSCRIPTION_REFRESH_INTERVAL"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.SubscriptionRefreshInterval = n
		}
	}
	if v := os.Getenv("HEALTH_CHECK_INTERVAL"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.HealthCheckInterval = n
		}
	}
	if v := os.Getenv("RUNTIME_MODE"); v != "" {
		cfg.RuntimeMode = v
	}
	if v := os.Getenv("POOL_ALGORITHM"); v != "" {
		cfg.PoolAlgorithm = v
	}
	if v := os.Getenv("DISPATCHER_ENABLED"); v != "" {
		cfg.Dispatcher.Enabled = v == "1" || strings.EqualFold(v, "true")
	}
	if v := os.Getenv("DISPATCHER_HTTP_LISTEN_ADDR"); v != "" {
		cfg.Dispatcher.HTTPListenAddr = v
	}
	if v := os.Getenv("DISPATCHER_SOCKS_LISTEN_ADDR"); v != "" {
		cfg.Dispatcher.SOCKSListenAddr = v
	}
	if v := os.Getenv("DISPATCHER_HTTP_LISTEN_PORT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.Dispatcher.HTTPListenPort = n
		}
	}
	if v := os.Getenv("DISPATCHER_SOCKS_LISTEN_PORT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.Dispatcher.SOCKSListenPort = n
		}
	}
	if v := os.Getenv("DISPATCHER_ALGORITHM"); v != "" {
		cfg.Dispatcher.Algorithm = v
	}
	if v := os.Getenv("PORTS_JSON"); v != "" {
		var ports []PortConfig
		if err := json.Unmarshal([]byte(v), &ports); err != nil {
			cfg.portsParseError = fmt.Errorf("invalid PORTS_JSON: %w", err)
		} else {
			cfg.Ports = ports
		}
	}

	return cfg
}

func (c Config) ResolvedPorts() []PortConfig {
	if len(c.Ports) == 0 {
		return []PortConfig{c.normalizePort(PortConfig{Key: DefaultPortKey})}
	}
	ports := make([]PortConfig, 0, len(c.Ports))
	for _, port := range c.Ports {
		ports = append(ports, c.normalizePort(port))
	}
	return ports
}

func (c Config) DefaultPort() PortConfig {
	ports := c.ResolvedPorts()
	for _, port := range ports {
		if port.Key == DefaultPortKey {
			return port
		}
	}
	return ports[0]
}

func (c Config) normalizePort(port PortConfig) PortConfig {
	if port.Key == "" {
		port.Key = DefaultPortKey
	}
	if port.Name == "" {
		port.Name = port.Key
	}
	if port.HTTPListenAddr == "" {
		port.HTTPListenAddr = c.HTTPListenAddr
	}
	if port.HTTPListenPort == 0 {
		port.HTTPListenPort = c.HTTPListenPort
	}
	if port.SOCKSListenAddr == "" {
		port.SOCKSListenAddr = c.SOCKSListenAddr
	}
	if port.SOCKSListenPort == 0 {
		port.SOCKSListenPort = c.SOCKSListenPort
	}
	if port.RuntimeMode == "" {
		port.RuntimeMode = c.RuntimeMode
	}
	if port.PoolAlgorithm == "" {
		port.PoolAlgorithm = c.PoolAlgorithm
	}
	port.Lanes = c.normalizeLanes(port)
	return port
}

func (c Config) normalizeLanes(port PortConfig) []LaneConfig {
	if len(port.Lanes) > 0 {
		lanes := make([]LaneConfig, 0, len(port.Lanes))
		for _, lane := range port.Lanes {
			if lane.ListenAddr == "" {
				if lane.Protocol == "socks" {
					lane.ListenAddr = port.SOCKSListenAddr
				} else {
					lane.ListenAddr = port.HTTPListenAddr
				}
			}
			lanes = append(lanes, lane)
		}
		return lanes
	}
	return []LaneConfig{
		{Key: "lane-http-1", Protocol: "http", ListenAddr: port.HTTPListenAddr, ListenPort: port.HTTPListenPort + 1001},
		{Key: "lane-socks-1", Protocol: "socks", ListenAddr: port.SOCKSListenAddr, ListenPort: port.SOCKSListenPort + 1001},
	}
}

func (c Config) Validate() error {
	if c.portsParseError != nil {
		return c.portsParseError
	}
	if c.HTTPListenPort <= 0 || c.SOCKSListenPort <= 0 || c.AdminListenPort <= 0 || c.HealthListenPort <= 0 {
		return fmt.Errorf("ports must be positive")
	}
	if c.DBPath == "" {
		return fmt.Errorf("db path is required")
	}
	if c.SingboxBinary == "" {
		return fmt.Errorf("sing-box binary is required")
	}
	if c.AdminUsername == "" {
		return fmt.Errorf("admin username is required")
	}
	if c.AdminPasswordHash == "" {
		return fmt.Errorf("admin password hash is required")
	}
	if err := validateRuntimeMode(c.RuntimeMode); err != nil {
		return err
	}
	if err := validatePoolAlgorithm(c.PoolAlgorithm); err != nil {
		return err
	}
	if err := validateDispatcherAlgorithm(c.Dispatcher.Algorithm); err != nil {
		return err
	}
	if c.AdminListenAddr == "" || c.HealthListenAddr == "" || c.HTTPListenAddr == "" || c.SOCKSListenAddr == "" {
		return fmt.Errorf("listen addresses are required")
	}

	listenBindings := map[string]string{}
	if err := ensureUniqueBinding(listenBindings, c.AdminListenAddr, c.AdminListenPort, "admin listen"); err != nil {
		return err
	}
	if err := ensureUniqueBinding(listenBindings, c.HealthListenAddr, c.HealthListenPort, "health listen"); err != nil {
		return err
	}
	if c.Dispatcher.Enabled {
		if c.Dispatcher.HTTPListenAddr == "" || c.Dispatcher.SOCKSListenAddr == "" {
			return fmt.Errorf("dispatcher listen addresses are required")
		}
		if c.Dispatcher.HTTPListenPort <= 0 || c.Dispatcher.SOCKSListenPort <= 0 {
			return fmt.Errorf("dispatcher listen ports must be positive")
		}
		if err := validateDispatcherRules(c.Dispatcher.Rules); err != nil {
			return err
		}
		if err := ensureUniqueBinding(listenBindings, c.Dispatcher.HTTPListenAddr, c.Dispatcher.HTTPListenPort, "dispatcher http listen"); err != nil {
			return err
		}
		if err := ensureUniqueBinding(listenBindings, c.Dispatcher.SOCKSListenAddr, c.Dispatcher.SOCKSListenPort, "dispatcher socks listen"); err != nil {
			return err
		}
	}

	ports := c.ResolvedPorts()
	seenKeys := map[string]struct{}{}
	containsDefault := false
	for _, port := range ports {
		if port.Key == DefaultPortKey {
			containsDefault = true
		}
		if !portKeyPattern.MatchString(port.Key) {
			return fmt.Errorf("invalid port key %q", port.Key)
		}
		if port.HTTPListenAddr == "" || port.SOCKSListenAddr == "" {
			return fmt.Errorf("port %q listen addresses are required", port.Key)
		}
		if port.HTTPListenPort <= 0 || port.SOCKSListenPort <= 0 {
			return fmt.Errorf("port %q listen ports must be positive", port.Key)
		}
		if err := validateRuntimeMode(port.RuntimeMode); err != nil {
			return fmt.Errorf("port %q: %w", port.Key, err)
		}
		if err := validatePoolAlgorithm(port.PoolAlgorithm); err != nil {
			return fmt.Errorf("port %q: %w", port.Key, err)
		}
		if _, ok := seenKeys[port.Key]; ok {
			return fmt.Errorf("duplicate port key %q", port.Key)
		}
		seenKeys[port.Key] = struct{}{}
		if err := ensureUniqueBinding(listenBindings, port.HTTPListenAddr, port.HTTPListenPort, fmt.Sprintf("port %q http listen", port.Key)); err != nil {
			return err
		}
		if err := ensureUniqueBinding(listenBindings, port.SOCKSListenAddr, port.SOCKSListenPort, fmt.Sprintf("port %q socks listen", port.Key)); err != nil {
			return err
		}
		seenLaneKeys := map[string]struct{}{}
		for _, lane := range port.Lanes {
			if lane.Key == "" {
				return fmt.Errorf("port %q lane key is required", port.Key)
			}
			if lane.Protocol != "http" && lane.Protocol != "socks" {
				return fmt.Errorf("port %q lane %q protocol must be http or socks", port.Key, lane.Key)
			}
			if lane.ListenAddr == "" || lane.ListenPort <= 0 {
				return fmt.Errorf("port %q lane %q listen binding is required", port.Key, lane.Key)
			}
			if _, ok := seenLaneKeys[lane.Key+":"+lane.Protocol]; ok {
				return fmt.Errorf("port %q duplicate lane %q/%s", port.Key, lane.Key, lane.Protocol)
			}
			seenLaneKeys[lane.Key+":"+lane.Protocol] = struct{}{}
			if err := ensureUniqueBinding(listenBindings, lane.ListenAddr, lane.ListenPort, fmt.Sprintf("port %q lane %q %s listen", port.Key, lane.Key, lane.Protocol)); err != nil {
				return err
			}
		}
	}
	if len(c.Ports) > 0 && !containsDefault {
		return fmt.Errorf("ports must include %q", DefaultPortKey)
	}
	return nil
}

func validateRuntimeMode(value string) error {
	if value != "single_active" && value != "pool" {
		return fmt.Errorf("runtime mode must be single_active or pool")
	}
	return nil
}

func validatePoolAlgorithm(value string) error {
	if value != "sequential" && value != "random" && value != "balance" {
		return fmt.Errorf("pool algorithm must be sequential, random, or balance")
	}
	return nil
}

func validateDispatcherAlgorithm(value string) error {
	if value != "sequential" && value != "random" && value != "balance" {
		return fmt.Errorf("dispatcher algorithm must be sequential, random, or balance")
	}
	return nil
}

func validateDispatcherRules(rules []DispatcherRuleConfig) error {
	seenNames := map[string]struct{}{}
	for _, rule := range rules {
		if rule.Name == "" {
			return fmt.Errorf("dispatcher rule name is required")
		}
		if _, ok := seenNames[rule.Name]; ok {
			return fmt.Errorf("duplicate dispatcher rule name %q", rule.Name)
		}
		seenNames[rule.Name] = struct{}{}
		if rule.HeaderName == "" && rule.HeaderValue != "" {
			return fmt.Errorf("dispatcher rule %q header_value requires header_name", rule.Name)
		}
		if rule.Host == "" && rule.HeaderName == "" {
			return fmt.Errorf("dispatcher rule %q must define host or header_name", rule.Name)
		}
		if rule.HeaderName != "" && rule.HeaderValue == "" {
			return fmt.Errorf("dispatcher rule %q header_value is required when header_name is set", rule.Name)
		}
		if rule.TargetPortKey == "" {
			return fmt.Errorf("dispatcher rule %q target_port_key is required", rule.Name)
		}
		if rule.TargetLaneKey == "" {
			return fmt.Errorf("dispatcher rule %q target_lane_key is required", rule.Name)
		}
	}
	return nil
}

func ensureUniqueBinding(bindings map[string]string, addr string, port int, label string) error {
	key := strings.ToLower(addr) + ":" + strconv.Itoa(port)
	if existing, ok := bindings[key]; ok {
		return fmt.Errorf("listen binding %s conflicts with %s", label, existing)
	}
	bindings[key] = label
	return nil
}
