package server

import (
	"encoding/base64"
	"errors"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

func protectSecret(value []byte) (string, error) {
	if len(value) == 0 {
		return "", nil
	}

	input := windows.DataBlob{Size: uint32(len(value)), Data: &value[0]}
	var output windows.DataBlob
	if err := windows.CryptProtectData(&input, nil, nil, 0, nil, 0, &output); err != nil {
		return "", err
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(output.Data)))

	protected := unsafe.Slice(output.Data, output.Size)
	return "dpapi:" + base64.StdEncoding.EncodeToString(protected), nil
}

func unprotectSecret(value string) ([]byte, error) {
	encoded, ok := strings.CutPrefix(value, "dpapi:")
	if !ok {
		return nil, errors.New("unsupported secret format")
	}
	protected, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, err
	}
	if len(protected) == 0 {
		return []byte{}, nil
	}

	input := windows.DataBlob{Size: uint32(len(protected)), Data: &protected[0]}
	var output windows.DataBlob
	if err := windows.CryptUnprotectData(&input, nil, nil, 0, nil, 0, &output); err != nil {
		return nil, err
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(output.Data)))

	secret := unsafe.Slice(output.Data, output.Size)
	result := make([]byte, len(secret))
	copy(result, secret)
	return result, nil
}
