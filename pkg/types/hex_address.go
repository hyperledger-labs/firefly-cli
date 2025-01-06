// Copyright © 2024 Kaleido, Inc.
//
// SPDX-License-Identifier: Apache-2.0
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package types

import (
	"encoding/hex"

	"github.com/hyperledger/firefly-signer/pkg/ethtypes"
	"gopkg.in/yaml.v3"
)

type HexAddress string

type HexWrapper struct {
	addrStr *ethtypes.Address0xHex
}

// WrapHexAddress wraps a hex address as HexAddress
func (h *HexWrapper) WrapHexAddress(addr [20]byte) (string, error) {
	hexStr := "0x" + hex.EncodeToString(addr[:])
	// Initialize addrStr before using it
	h.addrStr = new(ethtypes.Address0xHex)
	if err := h.addrStr.SetString(hexStr); err != nil {
		return "", err
	}
	return hexStr, nil
}

type HexType struct {
	HexValue HexAddress `yaml:"hexvalue"`
	HexWrap  HexWrapper
}

// Explicitly quote hex addresses so that they are interpreted as string (not int)
func (ht *HexType) MarshalYAML() (interface{}, error) {
	//convert to byte type
	hexBytes, err := hex.DecodeString(string(ht.HexValue[2:]))
	if err != nil {
		return nil, err
	}
	//copy bytes to fixed array
	var hexArray [20]byte
	copy(hexArray[:], hexBytes)
	hexAddr, err := ht.HexWrap.WrapHexAddress([20]byte(hexArray))
	if err != nil {
		return nil, err
	}
	return yaml.Node{
		Value: hexAddr,
		Kind:  yaml.ScalarNode,
		Style: yaml.DoubleQuotedStyle,
	}, nil
}
