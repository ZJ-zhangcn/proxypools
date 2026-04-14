package parser

import (
	"encoding/base64"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"

	"proxypools/internal/model"
)

func parseShareLinks(input []byte) ([]model.Node, error) {
	lines := strings.Split(strings.TrimSpace(string(input)), "\n")
	result := make([]model.Node, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		switch {
		case strings.HasPrefix(line, "ss://"):
			node, err := parseSS(line)
			if err != nil {
				return nil, err
			}
			result = append(result, node)
		case strings.HasPrefix(line, "trojan://"):
			node, err := parseTrojan(line)
			if err != nil {
				return nil, err
			}
			result = append(result, node)
		case strings.HasPrefix(line, "vless://"):
			node, err := parseVLESS(line)
			if err != nil {
				return nil, err
			}
			result = append(result, node)
		case strings.HasPrefix(line, "hysteria2://"):
			node, err := parseHysteria2(line)
			if err != nil {
				return nil, err
			}
			result = append(result, node)
		default:
			return nil, fmt.Errorf("unsupported share link: %s", line)
		}
	}
	return result, nil
}

func parseSS(line string) (model.Node, error) {
	raw := strings.TrimPrefix(strings.TrimSpace(line), "ss://")
	parts := strings.SplitN(raw, "#", 2)
	name := ""
	if len(parts) == 2 {
		decodedName, err := url.QueryUnescape(parts[1])
		if err == nil {
			name = decodedName
		}
	}

	mainPart := parts[0]
	if idx := strings.Index(mainPart, "?"); idx >= 0 {
		mainPart = mainPart[:idx]
	}

	userInfo := ""
	serverPart := ""
	if at := strings.LastIndex(mainPart, "@"); at >= 0 {
		userInfoEncoded := mainPart[:at]
		serverPart = mainPart[at+1:]
		decodedUserInfo, err := decodeSSUserInfo(userInfoEncoded)
		if err != nil {
			return model.Node{}, err
		}
		userInfo = decodedUserInfo
	} else {
		decoded, err := decodeBase64String(mainPart)
		if err != nil {
			return model.Node{}, err
		}
		userInfoAndServer := decoded
		at = strings.LastIndex(userInfoAndServer, "@")
		if at < 0 {
			return model.Node{}, fmt.Errorf("invalid shadowsocks link")
		}
		userInfo = userInfoAndServer[:at]
		serverPart = userInfoAndServer[at+1:]
	}

	credentials := strings.SplitN(userInfo, ":", 2)
	if len(credentials) != 2 {
		return model.Node{}, fmt.Errorf("invalid shadowsocks credentials")
	}

	host, portString, ok := strings.Cut(serverPart, ":")
	if !ok {
		return model.Node{}, fmt.Errorf("invalid shadowsocks server address")
	}
	port, err := strconv.Atoi(portString)
	if err != nil {
		return model.Node{}, err
	}

	payloadJSON, err := marshalPayload(map[string]any{
		"type":        "shadowsocks",
		"server":      host,
		"server_port": port,
		"method":      credentials[0],
		"password":    credentials[1],
	})
	if err != nil {
		return model.Node{}, err
	}

	if name == "" {
		name = host
	}

	return model.Node{
		SourceKey:    stableSourceKey("shadowsocks", host, portString, credentials[0], name),
		Name:         name,
		ProtocolType: "shadowsocks",
		Server:       host,
		Port:         port,
		PayloadJSON:  payloadJSON,
		Enabled:      true,
	}, nil
}

func parseTrojan(line string) (model.Node, error) {
	u, err := url.Parse(strings.TrimSpace(line))
	if err != nil {
		return model.Node{}, err
	}
	host, portString, err := net.SplitHostPort(u.Host)
	if err != nil {
		return model.Node{}, err
	}
	port, err := strconv.Atoi(portString)
	if err != nil {
		return model.Node{}, err
	}
	name := decodedFragment(u)
	if name == "" {
		name = host
	}
	query := u.Query()
	payload := map[string]any{
		"type":        "trojan",
		"server":      host,
		"server_port": port,
		"password":    passwordFromURL(u),
	}
	if network := strings.TrimSpace(query.Get("type")); network != "" && network != "ws" && network != "grpc" {
		payload["network"] = network
	}
	applyTLSAndTransport(payload, query)
	payloadJSON, err := marshalPayload(payload)
	if err != nil {
		return model.Node{}, err
	}
	return model.Node{
		SourceKey:    stableSourceKey("trojan", host, portString, name),
		Name:         name,
		ProtocolType: "trojan",
		Server:       host,
		Port:         port,
		PayloadJSON:  payloadJSON,
		Enabled:      true,
	}, nil
}

func parseVLESS(line string) (model.Node, error) {
	u, err := url.Parse(strings.TrimSpace(line))
	if err != nil {
		return model.Node{}, err
	}
	host, portString, err := net.SplitHostPort(u.Host)
	if err != nil {
		return model.Node{}, err
	}
	port, err := strconv.Atoi(portString)
	if err != nil {
		return model.Node{}, err
	}
	name := decodedFragment(u)
	if name == "" {
		name = host
	}
	query := u.Query()
	payload := map[string]any{
		"type":        "vless",
		"server":      host,
		"server_port": port,
		"uuid":        passwordFromURL(u),
	}
	if network := strings.TrimSpace(query.Get("type")); network != "" && network != "ws" && network != "grpc" {
		payload["network"] = network
	}
	applyTLSAndTransport(payload, query)
	payloadJSON, err := marshalPayload(payload)
	if err != nil {
		return model.Node{}, err
	}
	return model.Node{
		SourceKey:    stableSourceKey("vless", host, portString, name),
		Name:         name,
		ProtocolType: "vless",
		Server:       host,
		Port:         port,
		PayloadJSON:  payloadJSON,
		Enabled:      true,
	}, nil
}

func parseHysteria2(line string) (model.Node, error) {
	u, err := url.Parse(strings.TrimSpace(line))
	if err != nil {
		return model.Node{}, err
	}
	host, portString, err := net.SplitHostPort(u.Host)
	if err != nil {
		return model.Node{}, err
	}
	port, err := strconv.Atoi(portString)
	if err != nil {
		return model.Node{}, err
	}
	name := decodedFragment(u)
	if name == "" {
		name = host
	}
	query := u.Query()
	payload := map[string]any{
		"type":        "hysteria2",
		"server":      host,
		"server_port": port,
		"password":    passwordFromURL(u),
		"tls": map[string]any{
			"enabled": true,
		},
	}
	if obfsPassword := strings.TrimSpace(query.Get("obfs-password")); obfsPassword != "" {
		payload["obfs"] = map[string]any{"type": "salamander", "password": obfsPassword}
	}
	applyTLSAndTransport(payload, query)
	payloadJSON, err := marshalPayload(payload)
	if err != nil {
		return model.Node{}, err
	}
	return model.Node{
		SourceKey:    stableSourceKey("hysteria2", host, portString, name),
		Name:         name,
		ProtocolType: "hysteria2",
		Server:       host,
		Port:         port,
		PayloadJSON:  payloadJSON,
		Enabled:      true,
	}, nil
}

func applyTLSAndTransport(payload map[string]any, query url.Values) {
	if security := strings.TrimSpace(query.Get("security")); security == "reality" {
		tlsConfig := ensureTLS(payload)
		tlsConfig["enabled"] = true
		reality := map[string]any{"enabled": true}
		if publicKey := strings.TrimSpace(query.Get("pbk")); publicKey != "" {
			reality["public_key"] = publicKey
		}
		if shortID := strings.TrimSpace(query.Get("sid")); shortID != "" {
			reality["short_id"] = shortID
		}
		tlsConfig["reality"] = reality
	}
	if sni := strings.TrimSpace(query.Get("sni")); sni != "" {
		tlsConfig := ensureTLS(payload)
		tlsConfig["enabled"] = true
		tlsConfig["server_name"] = sni
	}
	if insecure := query.Get("allowInsecure"); insecure == "1" || strings.EqualFold(insecure, "true") {
		tlsConfig := ensureTLS(payload)
		tlsConfig["enabled"] = true
		tlsConfig["insecure"] = true
	}
	if insecure := query.Get("insecure"); insecure == "1" || strings.EqualFold(insecure, "true") {
		tlsConfig := ensureTLS(payload)
		tlsConfig["enabled"] = true
		tlsConfig["insecure"] = true
	}
	if fp := strings.TrimSpace(query.Get("fp")); fp != "" {
		tlsConfig := ensureTLS(payload)
		tlsConfig["enabled"] = true
		tlsConfig["utls"] = map[string]any{"enabled": true, "fingerprint": fp}
	}

	network := strings.TrimSpace(query.Get("type"))
	switch network {
	case "ws":
		transport := map[string]any{"type": "ws"}
		if path := strings.TrimSpace(query.Get("path")); path != "" {
			transport["path"] = path
		}
		if host := strings.TrimSpace(query.Get("host")); host != "" {
			transport["headers"] = map[string]any{"Host": host}
		}
		payload["transport"] = transport
	case "grpc":
		transport := map[string]any{"type": "grpc"}
		if serviceName := strings.TrimSpace(query.Get("serviceName")); serviceName != "" {
			transport["service_name"] = serviceName
		}
		payload["transport"] = transport
	}
}

func ensureTLS(payload map[string]any) map[string]any {
	if existing, ok := payload["tls"].(map[string]any); ok {
		return existing
	}
	tlsConfig := map[string]any{"enabled": true}
	payload["tls"] = tlsConfig
	return tlsConfig
}

func decodedFragment(u *url.URL) string {
	if u.Fragment == "" {
		return ""
	}
	decoded, err := url.QueryUnescape(u.Fragment)
	if err != nil {
		return u.Fragment
	}
	return decoded
}

func passwordFromURL(u *url.URL) string {
	if u.User == nil {
		return ""
	}
	if password, ok := u.User.Password(); ok {
		return password
	}
	return u.User.Username()
}

func decodeSSUserInfo(value string) (string, error) {
	if decoded, err := url.QueryUnescape(value); err == nil && strings.Contains(decoded, ":") {
		return decoded, nil
	}
	return decodeBase64String(value)
}

func decodeBase64String(value string) (string, error) {
	value = strings.TrimSpace(value)
	value = strings.Map(func(r rune) rune {
		switch r {
		case '\n', '\r', '\t', ' ':
			return -1
		default:
			return r
		}
	}, value)

	candidates := []string{value}
	if rem := len(value) % 4; rem != 0 {
		candidates = append(candidates, value+strings.Repeat("=", 4-rem))
	}

	for _, candidate := range candidates {
		for _, encoding := range []*base64.Encoding{base64.RawURLEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.StdEncoding} {
			decoded, err := encoding.DecodeString(candidate)
			if err == nil {
				return string(decoded), nil
			}
		}
	}
	return "", fmt.Errorf("decode base64 failed")
}
