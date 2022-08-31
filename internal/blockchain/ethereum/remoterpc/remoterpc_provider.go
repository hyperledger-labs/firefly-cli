// Copyright © 2022 Kaleido, Inc.
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

package remoterpc

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/hyperledger/firefly-cli/internal/blockchain/ethereum"
	"github.com/hyperledger/firefly-cli/internal/blockchain/ethereum/connector"
	"github.com/hyperledger/firefly-cli/internal/blockchain/ethereum/connector/ethconnect"
	"github.com/hyperledger/firefly-cli/internal/blockchain/ethereum/connector/evmconnect"
	"github.com/hyperledger/firefly-cli/internal/blockchain/ethereum/ethsigner"
	"github.com/hyperledger/firefly-cli/internal/constants"
	"github.com/hyperledger/firefly-cli/internal/docker"
	"github.com/hyperledger/firefly-cli/pkg/types"
)

type RemoteRPCProvider struct {
	ctx       context.Context
	stack     *types.Stack
	connector connector.Connector
	signer    *ethsigner.EthSignerProvider
}

func NewRemoteRPCProvider(ctx context.Context, stack *types.Stack) *RemoteRPCProvider {
	var connector connector.Connector
	switch stack.BlockchainConnector {
	case types.Ethconnect.String():
		connector = ethconnect.NewEthconnect(ctx)
	case types.Evmconnect.String():
		connector = evmconnect.NewEvmconnect(ctx)
	}

	return &RemoteRPCProvider{
		ctx:       ctx,
		stack:     stack,
		connector: connector,
		signer:    ethsigner.NewEthSignerProvider(ctx, stack),
	}
}

func (p *RemoteRPCProvider) WriteConfig(options *types.InitOptions) error {
	initDir := filepath.Join(constants.StacksDir, p.stack.Name, "init")
	for i, member := range p.stack.Members {

		// Generate the connector config for each member
		connectorConfigPath := filepath.Join(initDir, "config", fmt.Sprintf("%s_%v.yaml", p.connector.Name(), i))
		if err := p.connector.GenerateConfig(member, "ethsigner").WriteConfig(connectorConfigPath, options.ExtraConnectorConfigPath); err != nil {
			return err
		}

	}

	return p.signer.WriteConfig(options, options.RemoteNodeURL)
}

func (p *RemoteRPCProvider) FirstTimeSetup() error {
	if err := p.signer.FirstTimeSetup(); err != nil {
		return err
	}

	for i := range p.stack.Members {
		// Copy connector config to each member's volume
		connectorConfigPath := filepath.Join(p.stack.StackDir, "runtime", "config", fmt.Sprintf("%s_%v.yaml", p.connector.Name(), i))
		connectorConfigVolumeName := fmt.Sprintf("%s_%s_config_%v", p.stack.Name, p.connector.Name(), i)
		docker.CopyFileToVolume(p.ctx, connectorConfigVolumeName, connectorConfigPath, "config.yaml")
	}

	return nil
}

func (p *RemoteRPCProvider) PreStart() error {
	return nil
}

func (p *RemoteRPCProvider) PostStart(fistTimeSetup bool) error {
	return nil
}

func (p *RemoteRPCProvider) DeployFireFlyContract() (*types.ContractDeploymentResult, error) {
	return nil, fmt.Errorf("you must pre-deploy your FireFly contract when using a remote RPC endpoint")
}

func (p *RemoteRPCProvider) GetDockerServiceDefinitions() []*docker.ServiceDefinition {
	defs := []*docker.ServiceDefinition{
		p.signer.GetDockerServiceDefinition(p.stack.RemoteNodeURL),
	}
	defs = append(defs, p.connector.GetServiceDefinitions(p.stack, map[string]string{"ethsigner": "service_healthy"})...)
	return defs
}

func (p *RemoteRPCProvider) GetBlockchainPluginConfig(stack *types.Stack, m *types.Organization) (blockchainConfig *types.BlockchainConfig) {
	blockchainConfig = &types.BlockchainConfig{
		Type: "ethereum",
		Ethereum: &types.EthereumConfig{
			Ethconnect: &types.EthconnectConfig{
				URL:   p.GetConnectorURL(m),
				Topic: m.ID,
			},
		},
	}
	return
}

func (p *RemoteRPCProvider) GetOrgConfig(stack *types.Stack, m *types.Organization) (orgConfig *types.OrgConfig) {
	account := m.Account.(*ethereum.Account)
	orgConfig = &types.OrgConfig{
		Name: m.OrgName,
		Key:  account.Address,
	}
	return
}

func (p *RemoteRPCProvider) Reset() error {
	return nil
}

func (p *RemoteRPCProvider) GetContracts(filename string, extraArgs []string) ([]string, error) {
	contracts, err := ethereum.ReadContractJSON(filename)
	if err != nil {
		return []string{}, err
	}
	contractNames := make([]string, len(contracts.Contracts))
	i := 0
	for contractName := range contracts.Contracts {
		contractNames[i] = contractName
		i++
	}
	return contractNames, err
}

func (p *RemoteRPCProvider) DeployContract(filename, contractName, instanceName string, member *types.Organization, extraArgs []string) (*types.ContractDeploymentResult, error) {
	contracts, err := ethereum.ReadContractJSON(filename)
	if err != nil {
		return nil, err
	}
	return p.connector.DeployContract(contracts.Contracts[contractName], instanceName, member, extraArgs)
}

func (p *RemoteRPCProvider) CreateAccount(args []string) (interface{}, error) {
	return p.signer.CreateAccount(args)
}

func (p *RemoteRPCProvider) GetConnectorName() string {
	return p.connector.Name()
}

func (p *RemoteRPCProvider) GetConnectorURL(org *types.Organization) string {
	if !org.External {
		return fmt.Sprintf("http://%s_%s:%v", p.connector.Name(), org.ID, p.connector.Port())
	} else {
		return p.GetConnectorExternalURL(org)
	}
}

func (p *RemoteRPCProvider) GetConnectorExternalURL(org *types.Organization) string {
	return fmt.Sprintf("http://127.0.0.1:%v", org.ExposedConnectorPort)
}

func (p *RemoteRPCProvider) ParseAccount(account interface{}) interface{} {
	accountMap := account.(map[string]interface{})
	return &ethereum.Account{
		Address:    accountMap["address"].(string),
		PrivateKey: accountMap["privateKey"].(string),
	}
}
