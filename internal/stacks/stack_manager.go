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
package stacks

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/hyperledger/firefly-cli/internal/blockchain"
	"github.com/hyperledger/firefly-cli/internal/blockchain/ethereum/besu"
	"github.com/hyperledger/firefly-cli/internal/blockchain/ethereum/geth"
	"github.com/hyperledger/firefly-cli/internal/blockchain/ethereum/quorum"
	ethremoterpc "github.com/hyperledger/firefly-cli/internal/blockchain/ethereum/remoterpc"
	"github.com/hyperledger/firefly-cli/internal/blockchain/fabric"
	tezosremoterpc "github.com/hyperledger/firefly-cli/internal/blockchain/tezos/remoterpc"
	"github.com/hyperledger/firefly-cli/internal/constants"
	"github.com/hyperledger/firefly-cli/internal/core"
	"github.com/hyperledger/firefly-cli/internal/docker"
	"github.com/hyperledger/firefly-cli/internal/log"
	"github.com/hyperledger/firefly-cli/internal/tokens"
	"github.com/hyperledger/firefly-cli/internal/tokens/erc1155"
	"github.com/hyperledger/firefly-cli/internal/tokens/erc20erc721"
	"github.com/hyperledger/firefly-cli/pkg/types"
	"github.com/hyperledger/firefly-common/pkg/fftypes"
	"github.com/miracl/conflate"

	"gopkg.in/yaml.v3"

	"github.com/otiai10/copy"
)

type StackManager struct {
	ctx                context.Context
	Log                log.Logger
	Stack              *types.Stack
	blockchainProvider blockchain.IBlockchainProvider
	tokenProviders     []tokens.ITokensProvider
	IsOldFileStructure bool
	once               sync.Once
}

var unsupportedARM64Images map[string]bool = map[string]bool{
	"quorumengineering/tessera:24.4": true,
}

func ListStacks() ([]string, error) {
	files, err := os.ReadDir(constants.StacksDir)
	if err != nil {
		return nil, err
	}

	stacks := make([]string, 0, len(files))
	for _, f := range files {
		if f.IsDir() {
			if exists, err := CheckExists(f.Name()); err == nil && exists {
				stacks = append(stacks, f.Name())
			}
		}
	}
	return stacks, nil
}

func NewStackManager(ctx context.Context) *StackManager {
	return &StackManager{
		ctx: ctx,
		Log: log.LoggerFromContext(ctx),
	}
}

func (s *StackManager) InitStack(options *types.InitOptions) (err error) {
	environmentVarsMap := make(map[string]interface{})
	for key, value := range options.EnvironmentVars {
		environmentVarsMap[key] = value
	}
	s.Stack = &types.Stack{
		Name:                      options.StackName,
		Members:                   make([]*types.Organization, options.MemberCount),
		ExposedBlockchainPort:     options.ServicesBasePort,
		ExposedPtmPort:            options.PtmBasePort,
		Database:                  fftypes.FFEnum(options.DatabaseProvider),
		BlockchainProvider:        fftypes.FFEnum(options.BlockchainProvider),
		BlockchainNodeProvider:    fftypes.FFEnum(options.BlockchainNodeProvider),
		BlockchainConnector:       fftypes.FFEnum(options.BlockchainConnector),
		PrivateTransactionManager: fftypes.FFEnum(options.PrivateTransactionManager),
		Consensus:                 fftypes.FFEnum(options.Consensus),
		ContractAddress:           options.ContractAddress,
		StackDir:                  filepath.Join(constants.StacksDir, options.StackName),
		InitDir:                   filepath.Join(constants.StacksDir, options.StackName, "init"),
		RuntimeDir:                filepath.Join(constants.StacksDir, options.StackName, "runtime"),
		State: &types.StackState{
			DeployedContracts: make([]*types.DeployedContract, 0),
			Accounts:          make([]interface{}, options.MemberCount),
		},
		SandboxEnabled:    options.SandboxEnabled,
		MultipartyEnabled: options.MultipartyEnabled,
		ChainIDPtr:        &options.ChainID,
		RemoteNodeURL:     options.RemoteNodeURL,
		RequestTimeout:    options.RequestTimeout,
		IPFSMode:          fftypes.FFEnum(options.IPFSMode),
		ChannelName:       options.ChannelName,
		ChaincodeName:     options.ChaincodeName,
		CustomPinSupport:  options.CustomPinSupport,
		RemoteNodeDeploy:  options.RemoteNodeDeploy,
		EnvironmentVars:   environmentVarsMap,
	}

	tokenProviders, err := types.FFEnumArray(s.ctx, options.TokenProviders)
	if err != nil {
		return err
	}
	s.Stack.TokenProviders = tokenProviders

	if s.Stack.IPFSMode.Equals(types.IPFSModePrivate) {
		s.Stack.SwarmKey, err = GenerateSwarmKey()
		if err != nil {
			return err
		}
	}

	if options.PrometheusEnabled {
		s.Stack.PrometheusEnabled = true
		s.Stack.ExposedPrometheusPort = options.PrometheusPort
	}

	if len(options.CCPYAMLPaths) != 0 && len(options.MSPPaths) != 0 {
		s.Stack.RemoteFabricNetwork = true
	} else {
		s.Stack.ChannelName = "firefly"
		s.Stack.ChaincodeName = "firefly"
	}

	var manifest *types.VersionManifest

	if options.ManifestPath != "" {
		// If a path to a manifest file is set, read the existing file
		manifest, err = core.ReadManifestFile(s.ctx, options.ManifestPath)
		if err != nil {
			return err
		}
	} else {
		// Otherwise, fetch the manifest file from GitHub for the specified version
		if options.FireFlyVersion == "" || strings.ToLower(options.FireFlyVersion) == "latest" {
			manifest, err = core.GetManifestForChannel(fftypes.FFEnum(options.ReleaseChannel))
			if err != nil {
				return err
			}
		} else {
			manifest, err = core.GetManifestForRelease(options.FireFlyVersion)
			if err != nil {
				return err
			}
		}
	}

	s.Stack.VersionManifest = manifest
	s.blockchainProvider = s.getBlockchainProvider()
	s.tokenProviders = s.getITokenProviders()

	for i := 0; i < options.MemberCount; i++ {
		externalProcess := i < options.ExternalProcesses
		member, err := s.createMember(fmt.Sprint(i), i, options, externalProcess)
		if err != nil {
			return err
		}
		s.Stack.Members[i] = member
		if s.Stack.Members[i].Account != nil {
			s.Stack.State.Accounts[i] = s.Stack.Members[i].Account
		}
	}

	if err := s.ensureInitDirectories(); err != nil {
		return err
	}

	compose := s.buildDockerCompose()
	if err := s.writeDockerCompose(compose); err != nil {
		return fmt.Errorf("failed to write docker-compose.yml: %s", err)
	}
	if err := s.writeDockerComposeOverride(compose); err != nil {
		return fmt.Errorf("failed to write docker-compose.override.yml: %s", err)
	}
	return s.writeConfig(options)
}

func (s *StackManager) runDockerComposeCommand(command ...string) error {
	baseCompose := filepath.Join(s.Stack.StackDir, "docker-compose.yml")
	runtimeCompose := filepath.Join(s.Stack.RuntimeDir, "docker-compose.yml")
	if _, err := os.Stat(baseCompose); os.IsNotExist(err) {
		if _, err := os.Stat(runtimeCompose); err == nil {
			// Handle copying the docker-compose file out of the old "runtime" directory
			if err := copy.Copy(runtimeCompose, baseCompose); err != nil {
				return err
			}
		}
		return err
	}
	return docker.RunDockerComposeCommand(s.ctx, s.Stack.StackDir, command...)
}

func (s *StackManager) buildDockerCompose() *docker.DockerComposeConfig {
	compose := docker.CreateDockerCompose(s.Stack)
	extraServices := s.blockchainProvider.GetDockerServiceDefinitions()
	for i, tp := range s.tokenProviders {
		extraServices = append(extraServices, tp.GetDockerServiceDefinitions(i)...)
	}

	for _, serviceDefinition := range extraServices {
		// Add each service definition to the docker compose file
		compose.Services[serviceDefinition.ServiceName] = serviceDefinition.Service
		// Add the volume name for each volume used by this service
		for _, volumeName := range serviceDefinition.VolumeNames {
			compose.Volumes[volumeName] = struct{}{}
		}

		// Add a dependency so each firefly core container won't start up until dependencies are up
		for _, member := range s.Stack.Members {
			if service, ok := compose.Services[fmt.Sprintf("firefly_core_%v", *member.Index)]; ok {
				condition := "service_started"
				if serviceDefinition.Service.HealthCheck != nil {
					condition = "service_healthy"
				}
				service.DependsOn[serviceDefinition.ServiceName] = map[string]string{"condition": condition}
			}
		}
	}
	return compose
}

func CheckExists(stackName string) (bool, error) {
	_, err := os.Stat(filepath.Join(constants.StacksDir, stackName, "stack.json"))
	switch {
	case os.IsNotExist(err):
		return false, nil
	case err != nil:
		return false, err
	default:
		return true, nil
	}
}

func (s *StackManager) LoadStack(stackName string) error {
	stackDir := filepath.Join(constants.StacksDir, stackName)
	exists, err := CheckExists(stackName)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("stack '%s' does not exist", stackName)
	}
	d, err := os.ReadFile(filepath.Join(stackDir, "stack.json"))
	if err != nil {
		return err
	}
	var stack *types.Stack
	if err := json.Unmarshal(d, &stack); err != nil {
		return err
	}
	s.Stack = stack
	s.Stack.StackDir = stackDir
	s.blockchainProvider = s.getBlockchainProvider()
	s.tokenProviders = s.getITokenProviders()

	if s.Stack.RequestTimeout > 0 {
		core.SetRequestTimeout(s.Stack.RequestTimeout)
	}

	isOldFileStructure, err := s.Stack.IsOldFileStructure()
	if err != nil {
		return err
	}

	if !isOldFileStructure {
		s.Stack.InitDir = filepath.Join(s.Stack.StackDir, "init")
		s.Stack.RuntimeDir = filepath.Join(s.Stack.StackDir, "runtime")
	} else {
		s.IsOldFileStructure = true
		s.Stack.InitDir = s.Stack.StackDir
		s.Stack.RuntimeDir = s.Stack.StackDir
	}

	for _, member := range stack.Members {
		if member.Account != nil {
			member.Account = s.blockchainProvider.ParseAccount(member.Account)
		}
	}

	// For backwards compatibility, add a "default" VersionManifest
	// in memory for stacks that were created with old CLI versions
	if s.Stack.VersionManifest == nil {
		s.Stack.VersionManifest = &types.VersionManifest{
			FireFly: &types.ManifestEntry{
				Image: "ghcr.io/hyperledger/firefly",
				Tag:   "latest",
			},
			Ethconnect: &types.ManifestEntry{
				Image: "ghcr.io/hyperledger/firefly-ethconnect",
				Tag:   "latest",
			},
			Fabconnect: &types.ManifestEntry{
				Image: "ghcr.io/hyperledger/firefly-fabconnect",
				Tag:   "latest",
			},
			DataExchange: &types.ManifestEntry{
				Image: "ghcr.io/hyperledger/firefly-dataexchange-https",
				Tag:   "latest",
			},
			TokensERC1155: &types.ManifestEntry{
				Image: "ghcr.io/hyperledger/firefly-tokens-erc1155",
				Tag:   "latest",
			},
			TokensERC20ERC721: &types.ManifestEntry{
				Image: "ghcr.io/hyperledger/firefly-tokens-erc20-erc721",
				Tag:   "latest",
			},
		}
	}

	// If signer is not specified in the manifest, use the previously hardcoded default
	if s.Stack.VersionManifest.Signer == nil {
		s.Stack.VersionManifest.Signer = &types.ManifestEntry{
			Image: "ghcr.io/hyperledger/firefly-signer",
			Tag:   "v0.9.6",
		}
	}

	stackHasRunBefore, err := s.Stack.HasRunBefore()
	if err != nil {
		return nil
	}
	if stackHasRunBefore {
		return s.loadStackStateJSON()
	} else {
		s.Stack.State = &types.StackState{}
	}
	return nil
}

func (s *StackManager) loadStackStateJSON() error {
	stackStatePath := filepath.Join(s.Stack.RuntimeDir, "stackState.json")
	_, err := os.Stat(stackStatePath)
	if os.IsNotExist(err) {
		// Initialize with an empty StackState
		s.Stack.State = &types.StackState{}
		return nil
	} else if err != nil {
		return err
	}

	b, err := os.ReadFile(stackStatePath)
	if err != nil {
		return err
	}
	var stackState *types.StackState
	if err := json.Unmarshal(b, &stackState); err != nil {
		return err
	}

	for i, account := range stackState.Accounts {
		if account != nil {
			stackState.Accounts[i] = s.blockchainProvider.ParseAccount(account)
		}
	}

	s.Stack.State = stackState
	return nil
}

func (s *StackManager) writeStackStateJSON(directory string) error {
	stackStateBytes, err := json.MarshalIndent(s.Stack.State, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(directory, "stackState.json"), stackStateBytes, 0755)
}

func (s *StackManager) ensureInitDirectories() error {
	configDir := filepath.Join(s.Stack.InitDir, "config")

	if err := os.MkdirAll(configDir, 0755); err != nil {
		return err
	}

	for _, member := range s.Stack.Members {
		if err := os.MkdirAll(filepath.Join(configDir, "dataexchange_"+member.ID, "peer-certs"), 0755); err != nil {
			return err
		}
	}

	return nil
}

func (s *StackManager) writeDockerCompose(compose *docker.DockerComposeConfig) error {
	comments := "# This file is generated - DO NOT EDIT!\n# To override config, edit docker-compose.override.yml\n"
	bytes := []byte(comments)
	yamlBytes, err := yaml.Marshal(compose)
	if err != nil {
		return err
	}
	bytes = append(bytes, yamlBytes...)
	return os.WriteFile(filepath.Join(s.Stack.StackDir, "docker-compose.yml"), bytes, 0755)
}

func (s *StackManager) writeDockerComposeOverride(compose *docker.DockerComposeConfig) error {
	comments := "# Add custom config overrides here\n# See https://docs.docker.com/compose/extends\n"
	bytes := []byte(comments)
	yamlBytes, err := yaml.Marshal(map[string]interface{}{"version": compose.Version})
	if err != nil {
		return err
	}
	bytes = append(bytes, yamlBytes...)
	return os.WriteFile(filepath.Join(s.Stack.StackDir, "docker-compose.override.yml"), bytes, 0755)
}

func (s *StackManager) writeStackConfig() error {
	stackConfigBytes, err := json.MarshalIndent(s.Stack, "", " ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(s.Stack.StackDir, "stack.json"), stackConfigBytes, 0755)
}

func (s *StackManager) writeConfig(options *types.InitOptions) error {
	if err := s.writeDataExchangeCerts(); err != nil {
		return err
	}

	for _, member := range s.Stack.Members {
		config := core.NewFireflyConfig(s.Stack, member)

		// TODO: This code assumes that there is only one plugin instance per type. When we add support for
		// multiple namespaces, this code will likely have to change a lot
		blockchainConfig := s.blockchainProvider.GetBlockchainPluginConfig(s.Stack, member)
		blockchainConfig.Name = "blockchain0"
		config.Plugins.Blockchain = []*types.BlockchainConfig{
			blockchainConfig,
		}

		if config.Plugins.Tokens == nil {
			config.Plugins.Tokens = []*types.TokensConfig{}
		}

		for iTok, tp := range s.tokenProviders {
			tokenConfig := tp.GetFireflyConfig(member, iTok)
			tokenConfig.Name = tp.GetName()
			config.Plugins.Tokens = append(config.Plugins.Tokens, tokenConfig)
		}

		coreConfigFilename := filepath.Join(s.Stack.InitDir, "config", fmt.Sprintf("firefly_core_%s.yml", member.ID))
		if err := core.WriteFireflyConfig(config, coreConfigFilename, options.ExtraCoreConfigPath); err != nil {
			return err
		}
	}

	if err := s.writeStackConfig(); err != nil {
		return err
	}
	if err := s.writeStackStateJSON(s.Stack.InitDir); err != nil {
		return err
	}

	if err := s.blockchainProvider.WriteConfig(options); err != nil {
		return err
	}

	if s.Stack.PrometheusEnabled {
		promConfig := s.GeneratePrometheusConfig()
		configBytes, err := yaml.Marshal(promConfig)
		if err != nil {
			return err
		}
		if err := os.WriteFile(path.Join(s.Stack.InitDir, "config", "prometheus.yml"), configBytes, 0755); err != nil {
			return err
		}
	}

	return nil
}

func (s *StackManager) writeDataExchangeCerts() error {
	configDir := filepath.Join(s.Stack.InitDir, "config")
	const dataexchange = "dataexchange"
	for _, member := range s.Stack.Members {

		memberDXDir := path.Join(configDir, dataexchange+"_"+member.ID)

		// TODO: remove dependency on openssl here
		//nolint:gosec
		opensslCmd := exec.Command("openssl", "req", "-new", "-x509", "-nodes", "-days", "365", "-subj", fmt.Sprintf("/CN=%s_%s/O=member_%s", dataexchange, member.ID, member.ID), "-keyout", "key.pem", "-out", "cert.pem")
		opensslCmd.Dir = filepath.Join(configDir, dataexchange+"_"+member.ID)
		if err := opensslCmd.Run(); err != nil {
			return err
		}

		dataExchangeConfig := s.GenerateDataExchangeHTTPSConfig(member.ID)
		configBytes, err := json.Marshal(dataExchangeConfig)
		if err != nil {
			return err
		}
		if err := os.WriteFile(path.Join(memberDXDir, "config.json"), configBytes, 0755); err != nil {
			return err
		}
	}
	return nil
}

func (s *StackManager) copyDataExchangeConfigToVolumes() error {
	configDir := filepath.Join(s.Stack.RuntimeDir, "config")
	for _, member := range s.Stack.Members {
		// Copy files into docker volumes
		memberDXDir := path.Join(configDir, "dataexchange_"+member.ID)
		volumeName := fmt.Sprintf("%s_dataexchange_%s", s.Stack.Name, member.ID)
		if err := docker.MkdirInVolume(s.ctx, volumeName, "destinations"); err != nil {
			return err
		}
		if err := docker.MkdirInVolume(s.ctx, volumeName, "peers"); err != nil {
			return err
		}
		if err := docker.MkdirInVolume(s.ctx, volumeName, "peer-certs"); err != nil {
			return err
		}
		if err := docker.MkdirInVolume(s.ctx, volumeName, "blobs"); err != nil {
			return err
		}
		if err := docker.CopyFileToVolume(s.ctx, volumeName, path.Join(memberDXDir, "config.json"), "/config.json"); err != nil {
			return err
		}
		if err := docker.CopyFileToVolume(s.ctx, volumeName, path.Join(memberDXDir, "cert.pem"), "/cert.pem"); err != nil {
			return err
		}
		if err := docker.CopyFileToVolume(s.ctx, volumeName, path.Join(memberDXDir, "key.pem"), "/key.pem"); err != nil {
			return err
		}
	}
	return nil
}

func (s *StackManager) createMember(id string, index int, options *types.InitOptions, external bool) (*types.Organization, error) {
	serviceBase := options.ServicesBasePort + (index * 100)
	ptmBase := options.PtmBasePort + (index * 10)
	member := &types.Organization{
		ID:                         id,
		Index:                      &index,
		ExposedFireflyPort:         options.FireFlyBasePort + index,
		ExposedFireflyAdminSPIPort: serviceBase + 1, // note shared blockchain node is on zero
		ExposedConnectorPort:       serviceBase + 2,
		ExposedUIPort:              serviceBase + 3,
		ExposedDatabasePort:        serviceBase + 4,
		ExposePtmTpPort:            ptmBase,
		External:                   external,
		OrgName:                    options.OrgNames[index],
		NodeName:                   options.NodeNames[index],
	}

	nextPort := serviceBase + 5
	member.ExposedDataexchangePort = serviceBase + nextPort
	nextPort++
	member.ExposedIPFSApiPort = serviceBase + nextPort
	nextPort++
	member.ExposedIPFSGWPort = serviceBase + nextPort
	nextPort++

	if options.PrometheusEnabled {
		member.ExposedFireflyMetricsPort = nextPort
		nextPort++
		member.ExposedConnectorMetricsPort = nextPort
		nextPort++
	}
	for range options.TokenProviders {
		member.ExposedTokensPorts = append(member.ExposedTokensPorts, nextPort)
		nextPort++
	}

	account, err := s.blockchainProvider.CreateAccount([]string{member.OrgName, member.OrgName, strconv.Itoa(index)})
	if err != nil {
		return nil, err
	}
	member.Account = account

	if options.SandboxEnabled {
		member.ExposedSandboxPort = nextPort
	}
	return member, nil
}

func (s *StackManager) StartStack(options *types.StartOptions) (messages []string, err error) {
	fmt.Printf("starting FireFly stack '%s'... ", s.Stack.Name)

	// Check port availability
	err = s.checkPortsAvailable()
	if err != nil {
		return messages, err
	}

	hasBeenRun, err := s.Stack.HasRunBefore()
	if err != nil {
		return messages, err
	}
	if !hasBeenRun {
		// Pass FirstEvent to the first-time setup
		setupMessages, err := s.runFirstTimeSetup(&types.InitOptions{
			FromBlock: options.FromBlock,
		})
				messages = append(messages, setupMessages...)
		if err != nil {
			// Handle rollback if necessary
			if options.NoRollback {
				return messages, err
			} else {
				s.Log.Error(fmt.Errorf("an error occurred - rolling back changes"))
				resetErr := s.ResetStack()
				if resetErr != nil {
					return messages, fmt.Errorf("%s - error resetting stack: %s", err.Error(), resetErr.Error())
				}
				return messages, fmt.Errorf("%s - all changes rolled back", err.Error())
			}
		}
	} else {
		// Standard startup sequence
		err = s.runStartupSequence(false)
		if err != nil {
			return messages, err
		}
	}

	return messages, s.ensureFireflyNodesUp(true)
}

func (s *StackManager) PullStack(options *types.PullOptions) error {
	var images []string
	manifestImages := make(map[string]bool)

	// Collect FireFly docker image names
	for _, entry := range s.Stack.VersionManifest.Entries() {
		if entry != nil {
			fullImage := entry.GetDockerImageString()
			s.Log.Info(fmt.Sprintf("Manifest entry image='%s' local=%t", fullImage, entry.Local))
			manifestImages[fullImage] = true
			if entry.Local {
				continue
			}
			images = append(images, fullImage)
		}
	}

	images = append(images, constants.IPFSImageName)

	// Also pull postgres if we're using it
	if s.Stack.Database.Equals(types.DatabaseSelectionPostgres) {
		images = append(images, constants.PostgresImageName)
	}

	// Also pull the Sandbox if we're using it
	if s.Stack.SandboxEnabled {
		images = append(images, constants.SandboxImageName)
	}

	// Iterate over all images used by the blockchain provider
	for _, service := range s.blockchainProvider.GetDockerServiceDefinitions() {
		if !manifestImages[service.Service.Image] {
			images = append(images, service.Service.Image)
		}
	}

	// Iterate over all images used by the tokens provider
	for iTok, tp := range s.tokenProviders {
		for _, service := range tp.GetDockerServiceDefinitions(iTok) {
			if !manifestImages[service.Service.Image] {
				images = append(images, service.Service.Image)
			}
		}
	}

	// Use docker to pull every image - retry on failure
	hasPulled := map[string]bool{}
	for _, image := range images {
		if _, ok := hasPulled[image]; !ok {
			s.Log.Info(fmt.Sprintf("pulling '%s'", image))
			args := []string{"pull", image}
			if unsupportedARM64Images[image] && runtime.GOARCH == "arm64" {
				args = append(args, "--platform=linux/amd64")
			}
			if err := docker.RunDockerCommandRetry(s.ctx, s.Stack.InitDir, options.Retries, args[0:]...); err != nil {
				return err
			} else {
				hasPulled[image] = true
			}
		}
	}
	return nil
}

func (s *StackManager) removeVolumes() error {
	var volumes []string
	for _, service := range s.blockchainProvider.GetDockerServiceDefinitions() {
		volumes = append(volumes, service.VolumeNames...)
	}
	for iTok, tp := range s.tokenProviders {
		for _, service := range tp.GetDockerServiceDefinitions(iTok) {
			volumes = append(volumes, service.VolumeNames...)
		}
	}
	for volumeName := range docker.CreateDockerCompose(s.Stack).Volumes {
		volumes = append(volumes, volumeName)
	}
	for _, volumeName := range volumes {
		if err := docker.RunDockerCommand(s.ctx, "", "volume", "remove", fmt.Sprintf("%s_%s", s.Stack.Name, volumeName)); err != nil {
			if !strings.Contains(strings.ToLower(err.Error()), "no such volume") {
				return err
			}
		}
	}
	return nil
}

func (s *StackManager) runStartupSequence(firstTimeSetup bool) error {
	if err := s.blockchainProvider.PreStart(); err != nil {
		return err
	}

	s.Log.Info("starting FireFly dependencies")
	if err := s.runDockerComposeCommand("up", "-d"); err != nil {
		return err
	}

	if err := s.blockchainProvider.PostStart(firstTimeSetup); err != nil {
		return err
	}

	return nil
}

func (s *StackManager) StopStack() error {
	return s.runDockerComposeCommand("stop")
}

func (s *StackManager) ResetStack() error {
	if err := s.runDockerComposeCommand("down"); err != nil {
		return err
	}
	if err := os.RemoveAll(s.Stack.RuntimeDir); err != nil {
		return err
	}
	if err := s.blockchainProvider.Reset(); err != nil {
		return err
	}
	if err := s.removeVolumes(); err != nil {
		return err
	}
	return nil
}

func (s *StackManager) RemoveStack() error {
	if err := s.runDockerComposeCommand("down"); err != nil {
		return err
	}
	if err := s.removeVolumes(); err != nil {
		return err
	}
	return os.RemoveAll(s.Stack.StackDir)
}

func (s *StackManager) checkPortsAvailable() error {
	ports := make([]int, 1)
	ports[0] = s.Stack.ExposedBlockchainPort
	for _, member := range s.Stack.Members {

		ports = append(ports, member.ExposedConnectorPort)
		ports = append(ports, member.ExposedDatabasePort)
		ports = append(ports, member.ExposedUIPort)
		ports = append(ports, member.ExposedTokensPorts...)
		ports = append(ports, member.ExposePtmTpPort)

		if !member.External {
			ports = append(ports, member.ExposedFireflyAdminSPIPort)
			ports = append(ports, member.ExposedFireflyPort)
			ports = append(ports, member.ExposedFireflyMetricsPort)
		}
		ports = append(ports, member.ExposedDataexchangePort)
		ports = append(ports, member.ExposedIPFSApiPort)
		ports = append(ports, member.ExposedIPFSGWPort)
		if s.Stack.SandboxEnabled {
			ports = append(ports, member.ExposedSandboxPort)
		}
	}

	if s.Stack.PrometheusEnabled {
		ports = append(ports, s.Stack.ExposedPrometheusPort)
	}

	for _, port := range ports {
		available, err := checkPortAvailable(port)
		if err != nil {
			return err
		}
		if !available {
			return fmt.Errorf("port %d is unavailable. please check to see if another process is listening on that port", port)
		}
	}
	return nil
}

func checkPortAvailable(port int) (bool, error) {
	timeout := time.Millisecond * 500
	conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", fmt.Sprint(port)), timeout)

	if netError, ok := err.(net.Error); ok && netError.Timeout() {
		return true, nil
	}

	switch t := err.(type) {

	case *net.OpError:
		//nolint:gocritic // can't rewrite this as an if, because .(type) cannot be used outside a switch
		switch t := t.Unwrap().(type) {
		case *os.SyscallError:
			if t.Syscall == "connect" {
				return true, nil
			}
		}
		if t.Op == "dial" {
			return false, err
		} else if t.Op == "read" {
			return true, nil
		}

	case syscall.Errno:
		if t == syscall.ECONNREFUSED {
			return true, nil
		}
	}

	if conn != nil {
		defer conn.Close()
		return false, nil
	}
	return true, nil
}

//nolint:gocyclo // TODO: Breaking this function apart would be great for code tidiness, but it's not an urgent priority
func (s *StackManager) runFirstTimeSetup(options *types.InitOptions) (messages []string, err error) {
	configDir := filepath.Join(s.Stack.RuntimeDir, "config")

	for i := 0; i < len(s.Stack.Members); i++ {
		if s.Stack.Members[i].Account != nil {
			s.Stack.State.Accounts = append(s.Stack.State.Accounts, s.Stack.Members[i].Account)
		}
	}

	if err := copy.Copy(s.Stack.InitDir, s.Stack.RuntimeDir); err != nil {
		return messages, err
	}

	// Re-write the docker-compose config to temporarily short-circuit the core runtimes
	if err := s.disableFireflyCoreContainers(); err != nil {
		return messages, err
	}

	s.Log.Info("initializing blockchain node")
	if err := s.blockchainProvider.FirstTimeSetup(); err != nil {
		return messages, err
	}

	if s.Stack.PrometheusEnabled {
		s.Log.Info("copying prometheus.yml to prometheus_config")
		volumeName := fmt.Sprintf("%s_prometheus_config", s.Stack.Name)
		if err := docker.CopyFileToVolume(s.ctx, volumeName, path.Join(configDir, "prometheus.yml"), "/prometheus.yml"); err != nil {
			return messages, err
		}
	}

	if err := s.copyDataExchangeConfigToVolumes(); err != nil {
		return messages, err
	}

	pullOptions := &types.PullOptions{
		Retries: 2,
	}
	if err := s.PullStack(pullOptions); err != nil {
		return messages, err
	}

	if err := s.runStartupSequence(true); err != nil {
		return messages, err
	}

	for i, tp := range s.tokenProviders {
		if !s.Stack.DisableTokenFactories {
			result, err := tp.DeploySmartContracts(i)
			if err != nil {
				return messages, err
			}
			if result != nil {
				if result.Message != "" {
					messages = append(messages, result.Message)
				}
				s.Stack.State.DeployedContracts = append(s.Stack.State.DeployedContracts, result.DeployedContract)
			}
		}
	}

	newConfig := &types.FireflyConfig{
		Namespaces: &types.NamespacesConfig{
			Default: "default",
			Predefined: []*types.Namespace{
				{
					Name:        "default",
					Description: "Default predefined namespace",
					Plugins:     []string{"database0", "blockchain0", "dataexchange0", "sharedstorage0"},
				},
			},
		},
	}

	newConfig.Namespaces.Predefined[0].Plugins = append(newConfig.Namespaces.Predefined[0].Plugins, types.FFEnumArrayToStrings(s.Stack.TokenProviders)...)

	var contractDeploymentResult *types.ContractDeploymentResult
	if s.Stack.MultipartyEnabled {
		if s.Stack.ContractAddress == "" {
			s.Log.Info("deploying FireFly smart contracts")
			contractDeploymentResult, err = s.blockchainProvider.DeployFireFlyContract()
			if err != nil {
				return messages, err
			}

			var contractLocation interface{}
			if s.Stack.ContractAddress != "" {
				contractLocation = map[string]interface{}{
					"address": s.Stack.ContractAddress,
				}
			} else {
				contractLocation = contractDeploymentResult.DeployedContract.Location
			}

			options := make(map[string]interface{})
			if s.Stack.CustomPinSupport {
				options["customPinSupport"] = true
			}

			if newConfig.Namespaces.Predefined[0].Multiparty == nil {
				newConfig.Namespaces.Predefined[0].Multiparty = &types.MultipartyConfig{
					Enabled: true,
					Org:     orgConfig,
					Node: &types.NodeConfig{
						Name: member.NodeName,
					},
				}
			}

			newConfig.Namespaces.Predefined[0].Multiparty.Contract = []*types.ContractConfig{
				{
					Location:   contractLocation,
					FirstEvent: options.FromBlock,
					Options:    options,
				},
			}

			if contractDeploymentResult.Message != "" {
				messages = append(messages, contractDeploymentResult.Message)
			}
			s.Stack.State.DeployedContracts = append(s.Stack.State.DeployedContracts, contractDeploymentResult.DeployedContract)
			if err = s.writeStackStateJSON(s.Stack.RuntimeDir); err != nil {
				return messages, err
			}
		}
	}

	for _, member := range s.Stack.Members {
		orgConfig := s.blockchainProvider.GetOrgConfig(s.Stack, member)
		newConfig.Namespaces.Predefined[0].DefaultKey = orgConfig.Key

		if s.Stack.MultipartyEnabled {
			if s.Stack.ContractAddress == "" {
				s.Log.Info("deploying FireFly smart contracts")
				contractDeploymentResult, err = s.blockchainProvider.DeployFireFlyContract()
				if err != nil {
					return messages, err
				}

				var contractLocation interface{}
				if s.Stack.ContractAddress != "" {
					contractLocation = map[string]interface{}{
						"address": s.Stack.ContractAddress,
					}
				} else {
					contractLocation = contractDeploymentResult.DeployedContract.Location
				}

				options := make(map[string]interface{})
				if s.Stack.CustomPinSupport {
					options["customPinSupport"] = true
				}

				if newConfig.Namespaces.Predefined[0].Multiparty == nil {
					newConfig.Namespaces.Predefined[0].Multiparty = &types.MultipartyConfig{
						Enabled: true,
						Org:     orgConfig,
						Node: &types.NodeConfig{
							Name: member.NodeName,
						},
					}
				}

				newConfig.Namespaces.Predefined[0].Multiparty.Contract = []*types.ContractConfig{
					{
						Location:   contractLocation,
						FirstEvent: options.FromBlock,
						Options:    options,
					},
				}

				if contractDeploymentResult.Message != "" {
					messages = append(messages, contractDeploymentResult.Message)
				}
				s.Stack.State.DeployedContracts = append(s.Stack.State.DeployedContracts, contractDeploymentResult.DeployedContract)
				if err = s.writeStackStateJSON(s.Stack.RuntimeDir); err != nil {
					return messages, err
				}
			}
		}

		if err := s.patchFireFlyCoreConfigs(configDir, member, newConfig); err != nil {
			return messages, err
		}
	}

	// Re-write the docker-compose config again, in case new values have been added
	compose := s.buildDockerCompose()
	if err := s.writeDockerCompose(compose); err != nil {
		return messages, err
	}

	// Restart all containers now that we've finalized the runtime config
	s.Log.Info("restarting containers")
	if err := s.runDockerComposeCommand("stop"); err != nil {
		return messages, err
	}
	if err := s.runStartupSequence(false); err != nil {
		return messages, err
	}

	if err := s.ensureFireflyNodesUp(true); err != nil {
		return messages, err
	}

	if s.Stack.MultipartyEnabled {
		if s.Stack.ContractAddress == "" {
			s.Log.Info("registering FireFly identities")
			if err := s.registerFireflyIdentities(); err != nil {
				return messages, err
			}
		} else {
			messages = append(messages, "NOTE: You have selected to use a pre-existing FireFly smart contract, so you will need to register your org by calling the /network/organizations/self and the /network/nodes/self endpoints")
			return messages, nil
		}
	}

	s.Log.Info("initializing token providers")
	for iTok, tp := range s.tokenProviders {
		if err := tp.FirstTimeSetup(iTok); err != nil {
			return messages, err
		}
	}

	// Update the stack state with any new state that was created as a part of the setup process
	return messages, s.writeStackStateJSON(s.Stack.RuntimeDir)
}

func (s *StackManager) ensureFireflyNodesUp(firstTimeSetup bool) error {
	for _, member := range s.Stack.Members {
		if member.External {
			configFilename := filepath.Join(s.Stack.RuntimeDir, "config", fmt.Sprintf("firefly_core_%v.yml", member.ID))
			var port int
			if firstTimeSetup {
				port = member.ExposedFireflyAdminSPIPort
			} else {
				port = member.ExposedFireflyPort
			}
			// Check process running
			available, err := checkPortAvailable(port)
			if err != nil {
				return err
			}
			if available {
				s.Log.Info(fmt.Sprintf("please start your firefly core with the config file for this stack: firefly -f %s  ", configFilename))
				if err := s.waitForFireflyStart(port); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func (s *StackManager) waitForFireflyStart(port int) error {
	retries := 600
	retryPeriod := 1000 // ms
	retriesRemaining := retries
	for retriesRemaining > 0 {
		time.Sleep(time.Duration(retryPeriod) * time.Millisecond)
		available, err := checkPortAvailable(port)
		if err != nil {
			return err
		}
		if !available {
			return nil
		}
		retriesRemaining--
	}
	return fmt.Errorf("waited for %v seconds for firefly to start on port %v but it was never available", retries*retryPeriod/1000, port)
}

func (s *StackManager) UpgradeStack(version string, forceUpgrade bool) error {
	// stop the currently running stack
	if err := s.StopStack(); err != nil {
		return err
	}
	oldManifest := s.Stack.VersionManifest
	oldVersion, err := docker.GetImageLabel(fmt.Sprintf("%s@sha256:%s", oldManifest.FireFly.Image, oldManifest.FireFly.SHA), "tag")
	if err != nil {
		return err
	}

	if !forceUpgrade {
		if err := core.ValidateVersionUpgrade(oldVersion, version); err != nil {
			return err
		}
	}

	// get the version manifest for the new version
	newManifest, err := core.GetManifestForRelease(version)
	if err != nil {
		return err
	}

	if err := replaceVersions(oldManifest, newManifest, filepath.Join(s.Stack.StackDir, "docker-compose.yml")); err != nil {
		return err
	}

	s.Stack.VersionManifest = newManifest
	if err := s.writeStackConfig(); err != nil {
		return err
	}

	if err := s.PullStack(&types.PullOptions{}); err != nil {
		return err
	}

	return nil
}

func replaceVersions(oldManifest, newManifest *types.VersionManifest, filename string) error {
	b, err := os.ReadFile(filename)
	if err != nil {
		return err
	}
	s := string(b)

	old := oldManifest.FireFly.GetDockerImageString()
	new := newManifest.FireFly.GetDockerImageString()
	s = strings.ReplaceAll(s, old, new)

	old = oldManifest.Ethconnect.GetDockerImageString()
	new = newManifest.Ethconnect.GetDockerImageString()
	s = strings.ReplaceAll(s, old, new)

	old = oldManifest.Evmconnect.GetDockerImageString()
	new = newManifest.Evmconnect.GetDockerImageString()
	s = strings.ReplaceAll(s, old, new)

	// v1.2.x stacks may not have had a tezosconnect entry because it's new
	if oldManifest.Tezosconnect != nil {
		old = oldManifest.Tezosconnect.GetDockerImageString()
		new = newManifest.Tezosconnect.GetDockerImageString()
		s = strings.ReplaceAll(s, old, new)
	}

	old = oldManifest.Fabconnect.GetDockerImageString()
	new = newManifest.Fabconnect.GetDockerImageString()
	s = strings.ReplaceAll(s, old, new)

	old = oldManifest.DataExchange.GetDockerImageString()
	new = newManifest.DataExchange.GetDockerImageString()
	s = strings.ReplaceAll(s, old, new)

	old = oldManifest.TokensERC1155.GetDockerImageString()
	new = newManifest.TokensERC1155.GetDockerImageString()
	s = strings.ReplaceAll(s, old, new)

	old = oldManifest.TokensERC20ERC721.GetDockerImageString()
	new = newManifest.TokensERC20ERC721.GetDockerImageString()
	s = strings.ReplaceAll(s, old, new)

	old = oldManifest.Signer.GetDockerImageString()
	new = newManifest.Signer.GetDockerImageString()
	s = strings.ReplaceAll(s, old, new)

	return os.WriteFile(filename, []byte(s), 0755)
}

func (s *StackManager) PrintStackInfo() error {
	fmt.Print("\n")
	if err := s.runDockerComposeCommand("images"); err != nil {
		return err
	}
	fmt.Print("\n")
	if err := s.runDockerComposeCommand("ps"); err != nil {
		return err
	}
	fmt.Printf("\nYour docker compose file for this stack can be found at: %s\n\n", filepath.Join(s.Stack.StackDir, "docker-compose.yml"))
	return nil
}

// IsRunning prints to the stdout, the stack name and it status as "running" or "not_running".
func (s *StackManager) IsRunning() error {
	output, err := docker.RunDockerComposeCommandReturnsStdout(s.Stack.StackDir, "ps")
	if err != nil {
		return err
	}

	formatHeader := "\n %s\t%s\t  "
	formatBody := "\n %s\t%s\t"

	w := new(tabwriter.Writer)
	w.Init(os.Stdout, 8, 8, 8, '\t', 0)

	s.once.Do(func() {
		fmt.Fprintf(w, formatHeader, "STACK", "STATUS")
	})

	if strings.Contains(string(output), s.Stack.Name) { // if the output contains the stack name, it means the container is running.
		fmt.Fprintf(w, formatBody, s.Stack.Name, "running")
	} else {
		fmt.Fprintf(w, formatBody, s.Stack.Name, "not_running")
	}
	fmt.Fprintln(w)
	w.Flush()
	return nil
}

func (s *StackManager) disableFireflyCoreContainers() error {
	compose := s.buildDockerCompose()
	for _, member := range s.Stack.Members {
		if !member.External {
			// Temporarily set the entrypoint to not run anything
			compose.Services[fmt.Sprintf("firefly_core_%v", *member.Index)].EntryPoint = []string{"/bin/sh", "-c", "exit", "0"}
		}
	}
	return s.writeDockerCompose(compose)
}

func (s *StackManager) patchFireFlyCoreConfigs(workingDir string, org *types.Organization, newConfig *types.FireflyConfig) error {
	if newConfig != nil {
		newConfigBytes, err := yaml.Marshal(newConfig)
		if err != nil {
			return err
		}
		s.Log.Debug(fmt.Sprintf("patching config for %s: %v", org.ID, newConfig))
		configFile := path.Join(workingDir, fmt.Sprintf("firefly_core_%s.yml", org.ID))
		merger := conflate.New()
		if err := merger.AddFiles(configFile); err != nil {
			return fmt.Errorf("failed merging config %s", configFile)
		}
		if err := merger.AddData(newConfigBytes); err != nil {
			return fmt.Errorf("failed merging YAML '%v' into config: %s", newConfig, err)
		}
		s.Log.Info(fmt.Sprintf("updating %s config for new smart contract address", org.ID))
		configData, err := merger.MarshalYAML()
		if err != nil {
			return err
		}
		if err = os.WriteFile(configFile, configData, 0755); err != nil {
			return err
		}
	}

	return nil
}

func (s *StackManager) GetContracts(filename string, extraArgs []string) ([]string, error) {
	return s.blockchainProvider.GetContracts(filename, extraArgs)
}

func (s *StackManager) DeployContract(filename, contractName string, memberIndex int, extraArgs []string) (string, error) {
	result, err := s.blockchainProvider.DeployContract(filename, contractName, contractName, s.Stack.Members[memberIndex], extraArgs)
	if err != nil {
		return "", err
	}
	// Update the stackState.json file with the newly deployed contract
	deployedContract := &types.DeployedContract{
		Name:     contractName,
		Location: result.DeployedContract.Location,
	}
	s.Stack.State.DeployedContracts = append(s.Stack.State.DeployedContracts, deployedContract)
	if err = s.writeStackStateJSON(s.Stack.RuntimeDir); err != nil {
		return "", err
	}

	// Serialize the contract location to JSON to print on the command line
	b, err := json.MarshalIndent(result.DeployedContract.Location, "", "  ")
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func (s *StackManager) CreateAccount(args []string) (string, error) {
	newAccount, err := s.blockchainProvider.CreateAccount(args)
	if err != nil {
		return "", err
	}
	s.Stack.State.Accounts = append(s.Stack.State.Accounts, newAccount)
	if err = s.writeStackStateJSON(s.Stack.RuntimeDir); err != nil {
		return "", err
	}

	// Serialize the account to JSON to print on the command line
	b, err := json.MarshalIndent(newAccount, "", "  ")
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func (s *StackManager) getBlockchainProvider() blockchain.IBlockchainProvider {

	if s.Stack.BlockchainProvider.Equals(types.BlockchainNodeProviderGeth) {
		s.Stack.BlockchainProvider = types.BlockchainProviderEthereum
		s.Stack.BlockchainNodeProvider = types.BlockchainNodeProviderGeth
	}

	if s.Stack.BlockchainProvider.Equals(types.BlockchainNodeProviderQuorum) {
		s.Stack.BlockchainProvider = types.BlockchainProviderEthereum
		s.Stack.BlockchainNodeProvider = types.BlockchainNodeProviderQuorum
	}

	if s.Stack.BlockchainProvider.Equals(types.BlockchainNodeProviderBesu) {
		s.Stack.BlockchainProvider = types.BlockchainProviderEthereum
		s.Stack.BlockchainNodeProvider = types.BlockchainNodeProviderBesu
	}

	// Fallbacks for old stacks that don't have a specific blockchain connector set
	if s.Stack.BlockchainConnector == "" {
		switch s.Stack.BlockchainProvider {
		case types.BlockchainProviderEthereum, types.BlockchainNodeProviderGeth, types.BlockchainNodeProviderQuorum, types.BlockchainNodeProviderBesu:
			// Ethconnect used to be the only option for ethereum before it was configurable so set it as the fallback
			s.Stack.BlockchainConnector = types.BlockchainConnectorEthconnect
		case types.BlockchainProviderFabric:
			// Fabconnect is the only option for fabric before it was configurable so set it as the fallback
			s.Stack.BlockchainConnector = types.BlockchainConnectorFabconnect
		}
	}

	s.Stack.DisableTokenFactories = false

	switch s.Stack.BlockchainProvider {
	case types.BlockchainProviderEthereum:
		switch s.Stack.BlockchainNodeProvider {
		case types.BlockchainNodeProviderGeth:
			return geth.NewGethProvider(s.ctx, s.Stack)
		case types.BlockchainNodeProviderBesu:
			return besu.NewBesuProvider(s.ctx, s.Stack)
		case types.BlockchainNodeProviderQuorum:
			return quorum.NewQuorumProvider(s.ctx, s.Stack)
		case types.BlockchainNodeProviderRemoteRPC:
			s.Stack.DisableTokenFactories = true
			return ethremoterpc.NewRemoteRPCProvider(s.ctx, s.Stack)
		default:
			return nil
		}
	case types.BlockchainProviderTezos:
		s.Stack.DisableTokenFactories = true
		return tezosremoterpc.NewRemoteRPCProvider(s.ctx, s.Stack)
	case types.BlockchainProviderFabric:
		s.Stack.DisableTokenFactories = true
		return fabric.NewFabricProvider(s.ctx, s.Stack)
	}
	return nil
}

func (s *StackManager) getITokenProviders() []tokens.ITokensProvider {
	tps := make([]tokens.ITokensProvider, len(s.Stack.TokenProviders))
	for i, tp := range s.Stack.TokenProviders {
		switch tp {
		case types.TokenProviderERC1155:
			tps[i] = erc1155.NewERC1155Provider(s.ctx, s.Stack, s.getBlockchainProvider())
		case types.TokenProviderERC20ERC721:
			tps[i] = erc20erc721.NewERC20ERC721Provider(s.ctx, s.Stack, s.getBlockchainProvider())
		default:
			return nil
		}
	}
	return tps
}
