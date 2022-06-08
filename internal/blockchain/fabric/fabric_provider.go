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

package fabric

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"path"
	"path/filepath"

	"github.com/hyperledger/firefly-cli/internal/blockchain/fabric/fabconnect"
	"github.com/hyperledger/firefly-cli/internal/core"
	"github.com/hyperledger/firefly-cli/internal/docker"
	"github.com/hyperledger/firefly-cli/internal/log"
	"github.com/hyperledger/firefly-cli/pkg/types"
)

type Account struct {
	Name    string `json:"name"`
	OrgName string `json:"orgName"`
}

type FabricProvider struct {
	Verbose bool
	Log     log.Logger
	Stack   *types.Stack
}

//go:embed configtx.yaml
var configtxYaml string

func (p *FabricProvider) WriteConfig(options *types.InitOptions) error {
	blockchainDirectory := path.Join(p.Stack.InitDir, "blockchain")
	cryptogenYamlPath := path.Join(blockchainDirectory, "cryptogen.yaml")

	os.MkdirAll(blockchainDirectory, 0755)

	if err := WriteCryptogenConfig(len(p.Stack.Members), cryptogenYamlPath); err != nil {
		return err
	}
	if err := WriteNetworkConfig(path.Join(blockchainDirectory, "ccp.yaml")); err != nil {
		return err
	}
	if err := fabconnect.WriteFabconnectConfig(path.Join(blockchainDirectory, "fabconnect.yaml")); err != nil {
		return err
	}
	if err := p.writeConfigtxYaml(); err != nil {
		return err
	}

	return nil
}

func (p *FabricProvider) FirstTimeSetup() error {
	blockchainDirectory := path.Join(p.Stack.RuntimeDir, "blockchain")
	cryptogenYamlPath := path.Join(blockchainDirectory, "cryptogen.yaml")
	volumeName := fmt.Sprintf("%s_firefly_fabric", p.Stack.Name)

	if err := docker.CreateVolume(volumeName, p.Verbose); err != nil {
		return err
	}

	// Run cryptogen to generate MSP
	if err := docker.RunDockerCommand(blockchainDirectory, p.Verbose, p.Verbose,
		"run",
		"--platform", getDockerPlatform(),
		"--rm",
		"-v", fmt.Sprintf("%s:/etc/template.yml", cryptogenYamlPath),
		"-v", fmt.Sprintf("%s:/etc/firefly", volumeName),
		FabricToolsImageName,
		"cryptogen", "generate",
		"--config", "/etc/template.yml",
		"--output", "/etc/firefly/organizations",
	); err != nil {
		return err
	}

	// Generate genesis block
	if err := docker.RunDockerCommand(blockchainDirectory, p.Verbose, p.Verbose,
		"run",
		"--platform", getDockerPlatform(),
		"--rm",
		"-v", fmt.Sprintf("%s:/etc/firefly", volumeName),
		"-v", fmt.Sprintf("%s:/etc/hyperledger/fabric/configtx.yaml", path.Join(blockchainDirectory, "configtx.yaml")),
		FabricToolsImageName,
		"configtxgen",
		"-outputBlock", "/etc/firefly/firefly.block",
		"-profile", "SingleOrgApplicationGenesis",
		"-channelID", "firefly",
	); err != nil {
		return err
	}

	return nil
}

func (p *FabricProvider) DeployFireFlyContract() (*core.BlockchainConfig, *types.ContractDeploymentResult, error) {
	// No config patch YAML required for Fabric, as the chaincode name is pre-determined
	result, err := p.deploySmartContracts()
	return nil, result, err
}

func (p *FabricProvider) deploySmartContracts() (*types.ContractDeploymentResult, error) {
	packageFilename := path.Join(p.Stack.RuntimeDir, "contracts", "firefly_fabric.tar.gz")
	chaincode := "firefly"
	channel := "firefly"
	version := "1.0"

	if err := p.extractChaincode(); err != nil {
		return nil, err
	}

	if err := p.createChannel(); err != nil {
		return nil, err
	}

	if err := p.joinChannel(); err != nil {
		return nil, err
	}

	if err := p.installChaincode(packageFilename); err != nil {
		return nil, err
	}

	res, err := p.queryInstalled()
	if err != nil {
		return nil, err
	}
	if len(res.InstalledChaincodes) == 0 {
		return nil, fmt.Errorf("failed to find installed chaincode")
	}

	if err := p.approveChaincode(channel, chaincode, version, res.InstalledChaincodes[0].PackageID); err != nil {
		return nil, err
	}

	if err := p.commitChaincode(channel, chaincode, version); err != nil {
		return nil, err
	}

	// Register pre-created identities
	p.Log.Info("registering identities")
	for _, m := range p.Stack.Members {
		_, err := p.registerIdentity(m, m.OrgName)
		if err != nil {
			return nil, err
		}
	}

	result := &types.ContractDeploymentResult{
		DeployedContract: &types.DeployedContract{
			Name: "FireFly",
			Location: map[string]string{
				"channel":   channel,
				"chaincode": chaincode,
			},
		},
	}
	return result, nil
}

func (p *FabricProvider) PreStart() error {
	return nil
}

func (p *FabricProvider) PostStart() error {
	return nil
}

func (p *FabricProvider) GetDockerServiceDefinitions() []*docker.ServiceDefinition {
	serviceDefinitions := GenerateDockerServiceDefinitions(p.Stack)
	serviceDefinitions = append(serviceDefinitions, p.getFabconnectServiceDefinitions(p.Stack.Members)...)
	return serviceDefinitions
}

func (p *FabricProvider) GetFireflyConfig(stack *types.Stack, m *types.Member) (blockchainConfig *core.BlockchainConfig, orgConfig *core.OrgConfig) {
	orgConfig = &core.OrgConfig{
		Name: m.OrgName,
		Key:  m.OrgName,
	}

	blockchainConfig = &core.BlockchainConfig{
		Type: "fabric",
		Fabric: &core.FabricConfig{
			Fabconnect: &core.FabconnectConfig{
				URL:       p.getFabconnectUrl(m),
				Chaincode: "firefly",
				Channel:   "firefly",
				Signer:    m.OrgName,
				Topic:     m.ID,
			},
		},
	}
	return
}

func (p *FabricProvider) Reset() error {
	return nil
}

func (p *FabricProvider) getFabconnectServiceDefinitions(members []*types.Member) []*docker.ServiceDefinition {
	blockchainDirectory := path.Join(p.Stack.RuntimeDir, "blockchain")
	serviceDefinitions := make([]*docker.ServiceDefinition, len(members))
	for i, member := range members {
		serviceDefinitions[i] = &docker.ServiceDefinition{
			ServiceName: "fabconnect_" + member.ID,
			Service: &docker.Service{
				Image:         p.Stack.VersionManifest.Fabconnect.GetDockerImageString(),
				ContainerName: fmt.Sprintf("%s_fabconnect_%s", p.Stack.Name, member.ID),
				Command:       "-f /fabconnect/fabconnect.yaml",
				DependsOn: map[string]map[string]string{
					"fabric_ca":      {"condition": "service_started"},
					"fabric_peer":    {"condition": "service_started"},
					"fabric_orderer": {"condition": "service_started"},
				},
				Ports: []string{fmt.Sprintf("%d:3000", member.ExposedConnectorPort)},
				Volumes: []string{
					fmt.Sprintf("fabconnect_receipts_%s:/fabconnect/receipts", member.ID),
					fmt.Sprintf("fabconnect_events_%s:/fabconnect/events", member.ID),
					fmt.Sprintf("%s:/fabconnect/fabconnect.yaml", path.Join(blockchainDirectory, "fabconnect.yaml")),
					fmt.Sprintf("%s:/fabconnect/ccp.yaml", path.Join(blockchainDirectory, "ccp.yaml")),
					"firefly_fabric:/etc/firefly",
				},
				HealthCheck: &docker.HealthCheck{
					Test: []string{"CMD", "wget", "-O", "-", "http://localhost:3000/status"},
				},
				Logging: docker.StandardLogOptions,
			},
			VolumeNames: []string{
				"fabconnect_receipts_" + member.ID,
				"fabconnect_events_" + member.ID,
				"firefly_fabric",
			},
		}
	}
	return serviceDefinitions
}

func (p *FabricProvider) getFabconnectUrl(member *types.Member) string {
	if !member.External {
		return fmt.Sprintf("http://fabconnect_%s:3000", member.ID)
	} else {
		return fmt.Sprintf("http://127.0.0.1:%v", member.ExposedConnectorPort)
	}
}

func (p *FabricProvider) writeConfigtxYaml() error {
	filePath := path.Join(p.Stack.InitDir, "blockchain", "configtx.yaml")
	return ioutil.WriteFile(filePath, []byte(configtxYaml), 0755)
}

func (p *FabricProvider) createChannel() error {
	p.Log.Info("creating channel")
	stackDir := p.Stack.StackDir
	volumeName := fmt.Sprintf("%s_firefly_fabric", p.Stack.Name)
	return docker.RunDockerCommand(stackDir, p.Verbose, p.Verbose,
		"run",
		"--platform", getDockerPlatform(),
		"--rm",
		fmt.Sprintf("--network=%s_default", p.Stack.Name),
		"-v", fmt.Sprintf("%s:/etc/firefly", volumeName),
		FabricToolsImageName,
		"osnadmin", "channel", "join",
		"--channelID", "firefly",
		"--config-block", "/etc/firefly/firefly.block",
		"-o", "fabric_orderer:7053",
		"--ca-file", "/etc/firefly/organizations/ordererOrganizations/example.com/users/Admin@example.com/tls/ca.crt",
		"--client-cert", "/etc/firefly/organizations/ordererOrganizations/example.com/users/Admin@example.com/tls/client.crt",
		"--client-key", "/etc/firefly/organizations/ordererOrganizations/example.com/users/Admin@example.com/tls/client.key",
	)
}

func (p *FabricProvider) joinChannel() error {
	p.Log.Info("joining channel")
	stackDir := p.Stack.StackDir
	volumeName := fmt.Sprintf("%s_firefly_fabric", p.Stack.Name)
	return docker.RunDockerCommand(stackDir, p.Verbose, p.Verbose,
		"run",
		"--platform", getDockerPlatform(),
		"--rm",
		fmt.Sprintf("--network=%s_default", p.Stack.Name),
		"-v", fmt.Sprintf("%s:/etc/firefly", volumeName),
		"-e", "CORE_PEER_ADDRESS=fabric_peer:7051",
		"-e", "CORE_PEER_TLS_ENABLED=true",
		"-e", "CORE_PEER_TLS_ROOTCERT_FILE=/etc/firefly/organizations/peerOrganizations/org1.example.com/peers/fabric_peer.org1.example.com/tls/ca.crt",
		"-e", "CORE_PEER_LOCALMSPID=Org1MSP",
		"-e", "CORE_PEER_MSPCONFIGPATH=/etc/firefly/organizations/peerOrganizations/org1.example.com/users/Admin@org1.example.com/msp",
		FabricToolsImageName,
		"peer", "channel", "join",
		"-b", "/etc/firefly/firefly.block")
}

func (p *FabricProvider) extractChaincode() error {
	contractsDir := path.Join(p.Stack.RuntimeDir, "contracts")

	if err := os.MkdirAll(contractsDir, 0755); err != nil {
		return err
	}

	var containerName string
	for _, member := range p.Stack.Members {
		if !member.External {
			containerName = fmt.Sprintf("%s_firefly_core_%s", p.Stack.Name, member.ID)
			break
		}
	}
	if containerName == "" {
		return errors.New("unable to extract contracts from container - no valid firefly core containers found in stack")
	}
	p.Log.Info("extracting smart contracts")
	if err := docker.CopyFromContainer(containerName, "/firefly/contracts/firefly_fabric.tar.gz", path.Join(contractsDir, "firefly_fabric.tar.gz"), p.Verbose); err != nil {
		return err
	}
	return nil
}

func (p *FabricProvider) installChaincode(packageFilename string) error {
	p.Log.Info("installing chaincode")
	contractsDir := path.Join(p.Stack.RuntimeDir, "contracts")
	volumeName := fmt.Sprintf("%s_firefly_fabric", p.Stack.Name)
	return docker.RunDockerCommand(contractsDir, p.Verbose, p.Verbose,
		"run",
		"--platform", getDockerPlatform(),
		"--rm",
		fmt.Sprintf("--network=%s_default", p.Stack.Name),
		"-e", "CORE_PEER_ADDRESS=fabric_peer:7051",
		"-e", "CORE_PEER_TLS_ENABLED=true",
		"-e", "CORE_PEER_TLS_ROOTCERT_FILE=/etc/firefly/organizations/peerOrganizations/org1.example.com/peers/fabric_peer.org1.example.com/tls/ca.crt",
		"-e", "CORE_PEER_LOCALMSPID=Org1MSP",
		"-e", "CORE_PEER_MSPCONFIGPATH=/etc/firefly/organizations/peerOrganizations/org1.example.com/users/Admin@org1.example.com/msp",
		"-v", fmt.Sprintf("%s:/package.tar.gz", packageFilename),
		"-v", fmt.Sprintf("%s:/etc/firefly", volumeName),
		FabricToolsImageName,
		"peer", "lifecycle", "chaincode", "install", "/package.tar.gz",
	)
}

func (p *FabricProvider) queryInstalled() (*QueryInstalledResponse, error) {
	p.Log.Info("querying installed chaincode")
	volumeName := fmt.Sprintf("%s_firefly_fabric", p.Stack.Name)
	str, err := docker.RunDockerCommandBuffered(p.Stack.RuntimeDir, p.Verbose,
		"run",
		"--platform", getDockerPlatform(),
		"--rm",
		fmt.Sprintf("--network=%s_default", p.Stack.Name),
		"-e", "CORE_PEER_ADDRESS=fabric_peer:7051",
		"-e", "CORE_PEER_TLS_ENABLED=true",
		"-e", "CORE_PEER_TLS_ROOTCERT_FILE=/etc/firefly/organizations/peerOrganizations/org1.example.com/peers/fabric_peer.org1.example.com/tls/ca.crt",
		"-e", "CORE_PEER_LOCALMSPID=Org1MSP",
		"-e", "CORE_PEER_MSPCONFIGPATH=/etc/firefly/organizations/peerOrganizations/org1.example.com/users/Admin@org1.example.com/msp",
		"-v", fmt.Sprintf("%s:/etc/firefly", volumeName),
		FabricToolsImageName,
		"peer", "lifecycle", "chaincode", "queryinstalled",
		"--output", "json",
	)
	if err != nil {
		return nil, err
	}
	var res *QueryInstalledResponse
	err = json.Unmarshal([]byte(str), &res)
	if err != nil {
		return nil, err
	}
	return res, nil
}

func (p *FabricProvider) approveChaincode(channel, chaincode, version, packageId string) error {
	p.Log.Info("approving chaincode")
	volumeName := fmt.Sprintf("%s_firefly_fabric", p.Stack.Name)
	return docker.RunDockerCommand(p.Stack.RuntimeDir, p.Verbose, p.Verbose,
		"run",
		"--platform", getDockerPlatform(),
		"--rm",
		fmt.Sprintf("--network=%s_default", p.Stack.Name),
		"-e", "CORE_PEER_ADDRESS=fabric_peer:7051",
		"-e", "CORE_PEER_TLS_ENABLED=true",
		"-e", "CORE_PEER_TLS_ROOTCERT_FILE=/etc/firefly/organizations/peerOrganizations/org1.example.com/peers/fabric_peer.org1.example.com/tls/ca.crt",
		"-e", "CORE_PEER_LOCALMSPID=Org1MSP",
		"-e", "CORE_PEER_MSPCONFIGPATH=/etc/firefly/organizations/peerOrganizations/org1.example.com/users/Admin@org1.example.com/msp",
		"-v", fmt.Sprintf("%s:/etc/firefly", volumeName),
		FabricToolsImageName,
		"peer", "lifecycle", "chaincode", "approveformyorg",
		"-o", "fabric_orderer:7050",
		"--ordererTLSHostnameOverride", "fabric_orderer",
		"--channelID", channel,
		"--name", chaincode,
		"--version", version,
		"--package-id", packageId,
		"--sequence", "1",
		"--tls",
		"--cafile", "/etc/firefly/organizations/ordererOrganizations/example.com/orderers/fabric_orderer.example.com/msp/tlscacerts/tlsca.example.com-cert.pem",
	)
}

func (p *FabricProvider) commitChaincode(channel, chaincode, version string) error {
	p.Log.Info("committing chaincode")
	volumeName := fmt.Sprintf("%s_firefly_fabric", p.Stack.Name)
	return docker.RunDockerCommand(p.Stack.RuntimeDir, p.Verbose, p.Verbose,
		"run",
		"--platform", getDockerPlatform(),
		"--rm",
		fmt.Sprintf("--network=%s_default", p.Stack.Name),
		"-e", "CORE_PEER_ADDRESS=fabric_peer:7051",
		"-e", "CORE_PEER_TLS_ENABLED=true",
		"-e", "CORE_PEER_TLS_ROOTCERT_FILE=/etc/firefly/organizations/peerOrganizations/org1.example.com/peers/fabric_peer.org1.example.com/tls/ca.crt",
		"-e", "CORE_PEER_LOCALMSPID=Org1MSP",
		"-e", "CORE_PEER_MSPCONFIGPATH=/etc/firefly/organizations/peerOrganizations/org1.example.com/users/Admin@org1.example.com/msp",
		"-v", fmt.Sprintf("%s:/etc/firefly", volumeName),
		FabricToolsImageName,
		"peer", "lifecycle", "chaincode", "commit",
		"-o", "fabric_orderer:7050",
		"--ordererTLSHostnameOverride", "fabric_orderer",
		"--channelID", channel,
		"--name", chaincode,
		"--version", version,
		"--sequence", "1",
		"--tls",
		"--cafile", "/etc/firefly/organizations/ordererOrganizations/example.com/orderers/fabric_orderer.example.com/msp/tlscacerts/tlsca.example.com-cert.pem",
	)
}

func (p *FabricProvider) registerIdentity(member *types.Member, name string) (*Account, error) {
	res, err := fabconnect.CreateIdentity(fmt.Sprintf("http://127.0.0.1:%v", member.ExposedConnectorPort), name)
	if err != nil {
		return nil, err
	}
	_, err = fabconnect.EnrollIdentity(fmt.Sprintf("http://127.0.0.1:%v", member.ExposedConnectorPort), name, res.Secret)
	if err != nil {
		return nil, err
	}
	return &Account{
		Name:    name,
		OrgName: member.OrgName,
	}, nil
}

func (p *FabricProvider) GetContracts(filename string, extraArgs []string) ([]string, error) {
	return []string{filename}, nil
}

func (p *FabricProvider) DeployContract(filename, contractName string, member *types.Member, extraArgs []string) (*types.ContractDeploymentResult, error) {
	filename, err := filepath.Abs(filename)
	if err != nil {
		return nil, err
	}
	switch {
	case len(extraArgs) < 1:
		return nil, fmt.Errorf("channel not set. usage: ff deploy <stack_name> <filename> <channel> <chaincode> <version>")
	case len(extraArgs) < 2:
		return nil, fmt.Errorf("chaincode not set. usage: ff deploy <stack_name> <filename> <channel> <chaincode> <version>")
	case len(extraArgs) < 3:
		return nil, fmt.Errorf("version not set. usage: ff deploy <stack_name> <filename> <channel> <chaincode> <version>")
	}
	channel := extraArgs[0]
	chaincode := extraArgs[1]
	version := extraArgs[2]

	if err := p.installChaincode(filename); err != nil {
		return nil, err
	}

	res, err := p.queryInstalled()
	if err != nil {
		return nil, err
	}

	chaincodeInstalled := false
	packageID := ""
	for _, installedChaincode := range res.InstalledChaincodes {
		if installedChaincode.Label == chaincode {
			chaincodeInstalled = true
			packageID = installedChaincode.PackageID
			break
		}
	}

	if !chaincodeInstalled {
		return nil, fmt.Errorf("failed to find installed chaincode")
	}

	if err := p.approveChaincode(channel, chaincode, version, packageID); err != nil {
		return nil, err
	}

	if err := p.commitChaincode(channel, chaincode, version); err != nil {
		return nil, err
	}
	result := &types.ContractDeploymentResult{
		DeployedContract: &types.DeployedContract{
			Name: "FireFly",
			Location: map[string]string{
				"channel":   channel,
				"chaincode": chaincode,
			},
		},
	}
	return result, nil
}

func (p *FabricProvider) CreateAccount(args []string) (interface{}, error) {
	stackHasRunBefore, err := p.Stack.HasRunBefore()
	if err != nil {
		return nil, err
	}
	switch {
	case len(args) < 1:
		return "", fmt.Errorf("org name not set. usage: ff accounts create <stack_name> <org_name> <account_name>")
	case len(args) < 2:
		return "", fmt.Errorf("account name not set. usage: ff accounts create <stack_name> <org_name> <account_name>")
	}
	orgName := args[0]
	accountName := args[1]

	if stackHasRunBefore {
		// Find the FireFly member by the org name
		for _, member := range p.Stack.Members {
			if member.OrgName == orgName {
				return p.registerIdentity(member, accountName)
			}
		}
		return nil, fmt.Errorf("unable to find a FireFly org with name: '%s'", orgName)
	}
	return &Account{
		Name:    accountName,
		OrgName: orgName,
	}, nil
}

// As of release 2.4, Hyperledger Fabric only publishes amd64 images, but no arm64 specific images
func getDockerPlatform() string {
	return "linux/amd64"
}

func (p *FabricProvider) ParseAccount(account interface{}) interface{} {
	accountMap := account.(map[string]interface{})
	return &Account{
		Name:    accountMap["name"].(string),
		OrgName: accountMap["orgName"].(string),
	}
}
