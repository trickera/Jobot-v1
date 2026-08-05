//go:build !windows

package server

import (
	"encoding/base64"
	"errors"
	"strings"
)

func protectSecret(value []byte) (string, error) {
	return "plain-dev:" + base64.StdEncoding.EncodeToString(value), nil
}

func unprotectSecret(value string) ([]byte, error) {
	encoded, ok := strings.CutPrefix(value, "plain-dev:")
	if !ok {
		return nil, errors.New("unsupported secret format")
	}
	return base64.StdEncoding.DecodeString(encoded)
}
