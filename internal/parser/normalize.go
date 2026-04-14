package parser

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
)

func normalizeProtocol(protocol string) string {
	switch strings.ToLower(strings.TrimSpace(protocol)) {
	case "ss", "shadowsocks":
		return "shadowsocks"
	case "socks", "socks5":
		return "socks"
	case "vmess", "vless", "trojan", "http":
		return strings.ToLower(strings.TrimSpace(protocol))
	default:
		return strings.ToLower(strings.TrimSpace(protocol))
	}
}

func stableSourceKey(parts ...string) string {
	h := sha1.New()
	for _, part := range parts {
		_, _ = h.Write([]byte(part))
		_, _ = h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

func marshalPayload(payload map[string]any) (string, error) {
	buf, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	return string(buf), nil
}

func intFromAny(value any) (int, error) {
	switch v := value.(type) {
	case int:
		return v, nil
	case int32:
		return int(v), nil
	case int64:
		return int(v), nil
	case uint:
		return int(v), nil
	case uint32:
		return int(v), nil
	case uint64:
		return int(v), nil
	case float64:
		return int(v), nil
	case string:
		var port int
		_, err := fmt.Sscanf(strings.TrimSpace(v), "%d", &port)
		if err != nil {
			return 0, fmt.Errorf("parse int from %q: %w", v, err)
		}
		return port, nil
	default:
		return 0, fmt.Errorf("unsupported integer type %T", value)
	}
}

func stringFromAny(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case fmt.Stringer:
		return v.String()
	default:
		return fmt.Sprint(v)
	}
}
