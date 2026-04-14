package runtime

import (
	"encoding/json"
	"fmt"

	"proxypools/internal/model"
)

const defaultPortKey = "default"

type LaneBuildInput struct {
	Key          string
	Protocol     string
	ListenAddr   string
	ListenPort   int
	ActiveNodeID int64
}

type PortBuildInput struct {
	Key             string
	HTTPListenAddr  string
	HTTPPort        int
	SOCKSListenAddr string
	SOCKSPort       int
	ActiveNodeID    int64
	Lanes           []LaneBuildInput
}

type BuildInput struct {
	HTTPListenAddr   string
	HTTPPort         int
	SOCKSListenAddr  string
	SOCKSPort        int
	HealthListenAddr string
	HealthPort       int
	Nodes            []model.Node
	ActiveNodeID     int64
	Ports            []PortBuildInput
}

func BuildConfig(in BuildInput) (string, error) {
	selectorNodeTags := make([]string, 0, len(in.Nodes))
	outbounds := make([]map[string]any, 0, len(in.Nodes)+len(in.Ports)*4+4)
	for _, node := range in.Nodes {
		var outbound map[string]any
		if err := json.Unmarshal([]byte(node.PayloadJSON), &outbound); err != nil {
			return "", err
		}
		outbound["tag"] = fmt.Sprintf("node-%d", node.ID)
		if _, ok := outbound["type"]; !ok {
			outbound["type"] = node.ProtocolType
		}
		outbounds = append(outbounds, outbound)
		selectorNodeTags = append(selectorNodeTags, fmt.Sprintf("node-%d", node.ID))
	}

	ports := resolvePorts(in)
	inbounds := make([]map[string]any, 0, len(ports)*4+1)
	routeRules := make([]map[string]any, 0, len(ports)*4+1)
	for _, port := range ports {
		httpInboundTag, socksInboundTag := inboundTags(port.Key)
		httpSelectorTag, socksSelectorTag := selectorTags(port.Key)
		defaultTag := selectorDefaultTag(port.ActiveNodeID)

		inbounds = append(inbounds,
			map[string]any{"type": "http", "tag": httpInboundTag, "listen": port.HTTPListenAddr, "listen_port": port.HTTPPort},
			map[string]any{"type": "socks", "tag": socksInboundTag, "listen": port.SOCKSListenAddr, "listen_port": port.SOCKSPort},
		)
		outbounds = append(outbounds,
			map[string]any{"type": "selector", "tag": httpSelectorTag, "outbounds": selectorNodeTags, "default": defaultTag, "interrupt_exist_connections": false},
			map[string]any{"type": "selector", "tag": socksSelectorTag, "outbounds": selectorNodeTags, "default": defaultTag, "interrupt_exist_connections": false},
		)
		routeRules = append(routeRules,
			map[string]any{"inbound": []string{httpInboundTag}, "outbound": httpSelectorTag},
			map[string]any{"inbound": []string{socksInboundTag}, "outbound": socksSelectorTag},
		)

		for _, lane := range resolveLanes(port) {
			normalizedLane, err := normalizeLane(port, lane)
			if err != nil {
				return "", err
			}
			laneInbound := laneInboundTag(port.Key, normalizedLane.Key, normalizedLane.Protocol)
			laneSelector := laneSelectorTag(port.Key, normalizedLane.Key, normalizedLane.Protocol)
			inbounds = append(inbounds, map[string]any{
				"type":        normalizedLane.Protocol,
				"tag":         laneInbound,
				"listen":      normalizedLane.ListenAddr,
				"listen_port": normalizedLane.ListenPort,
			})
			outbounds = append(outbounds, map[string]any{
				"type":                        "selector",
				"tag":                         laneSelector,
				"outbounds":                   selectorNodeTags,
				"default":                     selectorDefaultTag(normalizedLane.ActiveNodeID),
				"interrupt_exist_connections": false,
			})
			routeRules = append(routeRules, map[string]any{"inbound": []string{laneInbound}, "outbound": laneSelector})
		}
	}

	healthListenAddr := in.HealthListenAddr
	if healthListenAddr == "" {
		healthListenAddr = "127.0.0.1"
	}
	inbounds = append(inbounds, map[string]any{"type": "http", "tag": "health-in", "listen": healthListenAddr, "listen_port": in.HealthPort})
	outbounds = append(outbounds,
		map[string]any{"type": "selector", "tag": "health-check", "outbounds": selectorNodeTags, "default": selectorDefaultTag(in.ActiveNodeID), "interrupt_exist_connections": false},
		map[string]any{"type": "direct", "tag": "direct"},
	)
	routeRules = append(routeRules, map[string]any{"inbound": []string{"health-in"}, "outbound": "health-check"})

	root := map[string]any{
		"log": map[string]any{
			"level":     "info",
			"timestamp": true,
		},
		"inbounds":  inbounds,
		"outbounds": outbounds,
		"route": map[string]any{
			"rules": routeRules,
			"final": "direct",
		},
		"experimental": map[string]any{
			"clash_api": map[string]any{
				"external_controller": "127.0.0.1:9090",
				"secret":              "",
			},
		},
	}

	buf, err := json.Marshal(root)
	if err != nil {
		return "", err
	}
	return string(buf), nil
}

func resolvePorts(in BuildInput) []PortBuildInput {
	if len(in.Ports) == 0 {
		return []PortBuildInput{normalizePort(in, PortBuildInput{
			Key:             defaultPortKey,
			HTTPListenAddr:  in.HTTPListenAddr,
			HTTPPort:        in.HTTPPort,
			SOCKSListenAddr: in.SOCKSListenAddr,
			SOCKSPort:       in.SOCKSPort,
			ActiveNodeID:    in.ActiveNodeID,
		})}
	}
	ports := make([]PortBuildInput, 0, len(in.Ports))
	for _, port := range in.Ports {
		ports = append(ports, normalizePort(in, port))
	}
	return ports
}

func resolveLanes(port PortBuildInput) []LaneBuildInput {
	return port.Lanes
}

func normalizePort(in BuildInput, port PortBuildInput) PortBuildInput {
	if port.Key == "" {
		port.Key = defaultPortKey
	}
	if port.HTTPListenAddr == "" {
		if in.HTTPListenAddr != "" {
			port.HTTPListenAddr = in.HTTPListenAddr
		} else {
			port.HTTPListenAddr = "0.0.0.0"
		}
	}
	if port.SOCKSListenAddr == "" {
		if in.SOCKSListenAddr != "" {
			port.SOCKSListenAddr = in.SOCKSListenAddr
		} else {
			port.SOCKSListenAddr = "0.0.0.0"
		}
	}
	if port.ActiveNodeID == 0 {
		port.ActiveNodeID = in.ActiveNodeID
	}
	return port
}

func normalizeLane(port PortBuildInput, lane LaneBuildInput) (LaneBuildInput, error) {
	if lane.Key == "" {
		return LaneBuildInput{}, fmt.Errorf("lane key is required for port %s", port.Key)
	}
	if lane.Protocol != "http" && lane.Protocol != "socks" {
		return LaneBuildInput{}, fmt.Errorf("lane protocol must be http or socks")
	}
	if lane.ListenAddr == "" {
		return LaneBuildInput{}, fmt.Errorf("lane listen addr is required for port %s lane %s", port.Key, lane.Key)
	}
	if lane.ListenPort <= 0 {
		return LaneBuildInput{}, fmt.Errorf("lane listen port must be positive for port %s lane %s", port.Key, lane.Key)
	}
	if lane.ActiveNodeID == 0 {
		lane.ActiveNodeID = port.ActiveNodeID
	}
	return lane, nil
}

func selectorDefaultTag(activeNodeID int64) string {
	if activeNodeID > 0 {
		return fmt.Sprintf("node-%d", activeNodeID)
	}
	return "direct"
}

func inboundTags(portKey string) (string, string) {
	if portKey == "" || portKey == defaultPortKey {
		return "http-in", "socks-in"
	}
	return "http-in-" + portKey, "socks-in-" + portKey
}

func selectorTags(portKey string) (string, string) {
	if portKey == "" || portKey == defaultPortKey {
		return "active-http", "active-socks"
	}
	return "active-http-" + portKey, "active-socks-" + portKey
}

func laneInboundTag(portKey string, laneKey string, protocol string) string {
	return fmt.Sprintf("%s-in-%s-%s", protocol, portKey, laneKey)
}

func laneSelectorTag(portKey string, laneKey string, protocol string) string {
	return fmt.Sprintf("active-%s-%s-%s", protocol, portKey, laneKey)
}

func LaneInboundTag(portKey string, laneKey string, protocol string) string {
	return laneInboundTag(portKey, laneKey, protocol)
}

func LaneSelectorTag(portKey string, laneKey string, protocol string) string {
	return laneSelectorTag(portKey, laneKey, protocol)
}
