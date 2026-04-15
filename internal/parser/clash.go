package parser

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
	"proxypools/internal/model"
)

type clashFile struct {
	Proxies []map[string]any `yaml:"proxies"`
}

func looksLikeClash(input []byte) bool {
	trimmed := strings.TrimSpace(string(input))
	return strings.HasPrefix(trimmed, "proxies:") || strings.Contains(trimmed, "\nproxies:")
}

func parseClash(input []byte) ([]model.Node, error) {
	var file clashFile
	if err := yaml.Unmarshal(input, &file); err != nil {
		return nil, err
	}

	result := make([]model.Node, 0, len(file.Proxies))
	for _, proxy := range file.Proxies {
		node, err := clashProxyToNode(proxy)
		if err != nil {
			return nil, err
		}
		result = append(result, node)
	}
	return result, nil
}

func clashProxyToNode(proxy map[string]any) (model.Node, error) {
	name := stringFromAny(proxy["name"])
	protocolType := normalizeProtocol(stringFromAny(proxy["type"]))
	server := stringFromAny(proxy["server"])
	port, err := intFromAny(proxy["port"])
	if err != nil {
		return model.Node{}, err
	}

	payload := map[string]any{
		"type":        protocolType,
		"server":      server,
		"server_port": port,
	}

	switch protocolType {
	case "vmess":
		payload["uuid"] = stringFromAny(proxy["uuid"])
		if security := strings.TrimSpace(stringFromAny(proxy["cipher"])); security != "" {
			payload["security"] = security
		} else {
			payload["security"] = "auto"
		}
	case "shadowsocks":
		payload["method"] = stringFromAny(proxy["cipher"])
		payload["password"] = stringFromAny(proxy["password"])
	case "socks":
		payload["version"] = "5"
		if username := strings.TrimSpace(stringFromAny(proxy["username"])); username != "" {
			payload["username"] = username
		}
		if password := strings.TrimSpace(stringFromAny(proxy["password"])); password != "" {
			payload["password"] = password
		}
	case "trojan":
		payload["password"] = stringFromAny(proxy["password"])
	case "vless":
		payload["uuid"] = stringFromAny(proxy["uuid"])
	case "anytls":
		password := strings.TrimSpace(stringFromAny(proxy["password"]))
		if password == "" {
			return model.Node{}, fmt.Errorf("invalid anytls clash proxy payload")
		}
		payload["password"] = password
		payload["tls"] = map[string]any{"enabled": true}
		if network := strings.TrimSpace(stringFromAny(proxy["network"])); network != "" && network != "tcp" {
			return model.Node{}, fmt.Errorf("unsupported anytls clash network: %s", network)
		}
		if sni := strings.TrimSpace(stringFromAny(proxy["sni"])); sni != "" {
			payload["tls"] = map[string]any{"enabled": true, "server_name": sni}
		} else if sni := strings.TrimSpace(stringFromAny(proxy["servername"])); sni != "" {
			payload["tls"] = map[string]any{"enabled": true, "server_name": sni}
		}
		if skipVerify, ok := proxy["skip-cert-verify"].(bool); ok && skipVerify {
			tlsConfig := ensureTLS(payload)
			tlsConfig["insecure"] = true
		}
		if fp := strings.TrimSpace(stringFromAny(proxy["client-fingerprint"])); fp != "" {
			tlsConfig := ensureTLS(payload)
			tlsConfig["utls"] = map[string]any{"enabled": true, "fingerprint": fp}
		}
		if alpn := parseALPN(anySliceToStrings(proxy["alpn"])); len(alpn) > 0 {
			tlsConfig := ensureTLS(payload)
			tlsConfig["alpn"] = alpn
		}
		if value := strings.TrimSpace(stringFromAny(proxy["idle-session-check-interval"])); value != "" {
			payload["idle_session_check_interval"] = value
		}
		if value := strings.TrimSpace(stringFromAny(proxy["idle-session-timeout"])); value != "" {
			payload["idle_session_timeout"] = value
		}
		if value, err := intFromAny(proxy["min-idle-session"]); err == nil && value > 0 {
			payload["min_idle_session"] = value
		}
	}

	if network := strings.TrimSpace(stringFromAny(proxy["network"])); network != "" && network != "ws" && network != "grpc" && protocolType != "anytls" {
		payload["network"] = network
	}
	if protocolType != "anytls" {
		if tlsFlag, ok := proxy["tls"].(bool); ok && tlsFlag {
			payload["tls"] = map[string]any{"enabled": true}
		}
		if sni := strings.TrimSpace(stringFromAny(proxy["servername"])); sni != "" {
			tlsConfig := map[string]any{"enabled": true, "server_name": sni}
			payload["tls"] = tlsConfig
		}
	}
	if network := strings.TrimSpace(stringFromAny(proxy["network"])); network == "ws" {
		transport := map[string]any{"type": "ws"}
		if path := strings.TrimSpace(stringFromAny(proxy["ws-path"])); path != "" {
			transport["path"] = path
		}
		if host := strings.TrimSpace(stringFromAny(proxy["ws-headers"])); host != "" {
			transport["headers"] = map[string]any{"Host": host}
		}
		payload["transport"] = transport
	}
	payloadJSON, err := marshalPayload(payload)
	if err != nil {
		return model.Node{}, err
	}
	if name == "" || protocolType == "" || server == "" || port <= 0 {
		return model.Node{}, fmt.Errorf("invalid clash proxy payload")
	}

	return model.Node{
		SourceKey:    stableSourceKey(protocolType, server, fmt.Sprintf("%d", port), name),
		Name:         name,
		ProtocolType: protocolType,
		Server:       server,
		Port:         port,
		PayloadJSON:  payloadJSON,
		Enabled:      true,
	}, nil
}
