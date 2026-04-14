package parser

import (
	"bytes"
	"strings"

	"proxypools/internal/model"
)

func ParseSubscription(input []byte) ([]model.Node, error) {
	trimmed := bytes.TrimSpace(input)
	if looksLikeClash(trimmed) {
		return parseClash(trimmed)
	}

	nodes, err := parseShareLinks(trimmed)
	if err == nil {
		return nodes, nil
	}

	decoded, decodeErr := decodeBase64String(strings.TrimSpace(string(trimmed)))
	if decodeErr == nil {
		return parseShareLinks([]byte(decoded))
	}
	return nil, err
}
