// Copyright © 2021 Kaleido, Inc.
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

package blockchain

import (
	"github.com/hyperledger/firefly-cli/internal/core"
	"github.com/hyperledger/firefly-cli/internal/docker"
	"github.com/hyperledger/firefly-cli/pkg/types"
)

type IBlockchainProvider interface {
	WriteConfig() error
	FirstTimeSetup() error
	DeploySmartContracts() error
	PreStart() error
	PostStart() error
	GetDockerServiceDefinitions() []*docker.ServiceDefinition
	GetFireflyConfig(m *types.Member) (blockchainConfig *core.BlockchainConfig, coreConfig *core.OrgConfig)
	Reset() error
	GetContracts(filename string) ([]string, error)
	DeployContract(filename, contractName string, member types.Member) (string, error)
}
