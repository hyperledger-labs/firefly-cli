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

package cmd

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/hyperledger/firefly-cli/internal/log"
	"github.com/hyperledger/firefly-cli/internal/stacks"
	"github.com/hyperledger/firefly-cli/pkg/types"
	"github.com/hyperledger/firefly-common/pkg/fftypes"
)

var initOptions types.InitOptions
var promptNames bool

var ffNameValidator = regexp.MustCompile(`^[0-9a-zA-Z]([0-9a-zA-Z._-]{0,62}[0-9a-zA-Z])?$`)

var stackNameInvalidRegex = regexp.MustCompile(`[^-_a-z0-9]`)

var initCmd = &cobra.Command{
	Use:   "init [stack_name] [member_count]",
	Short: "Create a new FireFly local dev stack",
	Long:  `Create a new FireFly local dev stack`,
	Args:  cobra.MaximumNArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := log.WithVerbosity(context.Background(), verbose)
		ctx = log.WithLogger(ctx, logger)
		stackManager := stacks.NewStackManager(ctx)
		if err := initCommon(args); err != nil {
			return err
		}
		if err := stackManager.InitStack(&initOptions); err != nil {
			if cleanupErr := stackManager.RemoveStack(); cleanupErr != nil {
				fmt.Printf("Cleanup from previous error returned: %s", cleanupErr)
			}
			return err
		}
		fmt.Printf("Stack '%s' created!\nTo start your new stack run:\n\n%s start %s\n", initOptions.StackName, rootCmd.Use, initOptions.StackName)
		fmt.Printf("\nYour docker compose file for this stack can be found at: %s\n\n", filepath.Join(stackManager.Stack.StackDir, "docker-compose.yml"))
		return nil
	},
}

func initCommon(args []string) error {
	if err := validateDatabaseProvider(initOptions.DatabaseProvider); err != nil {
		return err
	}
	if err := validateBlockchainProvider(initOptions.BlockchainProvider, initOptions.BlockchainNodeProvider); err != nil {
		return err
	}
	if err := validateTokensProvider(initOptions.TokenProviders, initOptions.BlockchainNodeProvider); err != nil {
		return err
	}
	if err := validateReleaseChannel(initOptions.ReleaseChannel); err != nil {
		return err
	}
	if err := validateIPFSMode(initOptions.IPFSMode); err != nil {
		return err
	}
	if err := validateConsensus(initOptions.Consensus); err != nil {
		return err
	}
	if err := validatePrivateTransactionManagerSelection(initOptions.PrivateTransactionManager, initOptions.BlockchainNodeProvider); err != nil {
		return err
	}
	if err := validatePrivateTransactionManagerBlockchainConnectorCombination(initOptions.PrivateTransactionManager, initOptions.BlockchainConnector); err != nil {
		return err
	}

	fmt.Println("initializing new FireFly stack...")

	if len(args) > 0 {
		initOptions.StackName = args[0]
		err := validateStackName(initOptions.StackName)
		if err != nil {
			return err
		}
	} else {
		initOptions.StackName, _ = prompt("stack name: ", validateStackName)
		fmt.Println("You selected " + initOptions.StackName)
	}

	var memberCountInput string
	if len(args) > 1 {
		memberCountInput = args[1]
		if err := validateCount(memberCountInput); err != nil {
			return err
		}
		memberCount, _ := strconv.Atoi(memberCountInput)
		initOptions.MemberCount = memberCount
	} else if initOptions.MemberCount == 0 {
		memberCountInput, _ = prompt("number of members: ", validateCount)
		memberCount, _ := strconv.Atoi(memberCountInput)
		initOptions.MemberCount = memberCount
	}

	orgNames := make([]string, initOptions.MemberCount)
	nodeNames := make([]string, initOptions.MemberCount)
	if promptNames {
		for i := 0; i < initOptions.MemberCount; i++ {
			name, _ := prompt(fmt.Sprintf("name for org %d: ", i), validateFFName)
			orgNames[i] = name
			name, _ = prompt(fmt.Sprintf("name for node %d: ", i), validateFFName)
			nodeNames[i] = name
		}
	} else {
		for i := 0; i < initOptions.MemberCount; i++ {
			randomName, err := randomHexString(3)
			if err != nil {
				return err
			}
			if len(initOptions.OrgNames) <= i || initOptions.OrgNames[i] == "" {
				orgNames[i] = fmt.Sprintf("org_%s", randomName)
			} else {
				orgNames[i] = initOptions.OrgNames[i]
			}
			if len(initOptions.NodeNames) <= i || initOptions.NodeNames[i] == "" {
				nodeNames[i] = fmt.Sprintf("node_%s", randomName)
			} else {
				nodeNames[i] = initOptions.NodeNames[i]
			}
		}
	}
	initOptions.OrgNames = orgNames
	initOptions.NodeNames = nodeNames

	return nil
}

func validateStackName(stackName string) error {
	if strings.TrimSpace(stackName) == "" {
		return errors.New("stack name must not be empty")
	}

	if stackNameInvalidRegex.Find([]byte(stackName)) != nil {
		return fmt.Errorf("stack name may not contain any character matching the regex: %s", stackNameInvalidRegex)
	}

	if exists, err := stacks.CheckExists(stackName); exists {
		return fmt.Errorf("stack '%s' already exists", stackName)
	} else {
		return err
	}
}

func validateCount(input string) error {
	if i, err := strconv.Atoi(input); err != nil {
		return errors.New("invalid number")
	} else if i <= 0 {
		return errors.New("number of members must be greater than zero")
	} else if initOptions.ExternalProcesses >= i {
		return errors.New("number of external processes should not be equal to or greater than the number of members in the network - at least one FireFly core container must exist to be able to extract and deploy smart contracts")
	}
	return nil
}

func validateFFName(input string) error {
	if !ffNameValidator.MatchString(input) {
		return fmt.Errorf("name must be 1-64 characters, including alphanumerics (a-zA-Z0-9), dot (.), dash (-) and underscore (_), and must start/end in an alphanumeric")
	}
	return nil
}

func validateDatabaseProvider(input string) error {
	_, err := fftypes.FFEnumParseString(context.Background(), types.DatabaseSelection, input)
	return err
}

func validateBlockchainProvider(providerString, nodeString string) error {
	_, err := fftypes.FFEnumParseString(context.Background(), types.BlockchainProvider, providerString)
	if err != nil {
		return nil
	}

	v, err := fftypes.FFEnumParseString(context.Background(), types.BlockchainNodeProvider, nodeString)
	if err != nil {
		return nil
	}

	if v == types.BlockchainProviderCorda {
		return errors.New("support for corda is coming soon")
	}

	return nil
}

func validateConsensus(consensusString string) error {
	v, err := fftypes.FFEnumParseString(context.Background(), types.Consensus, consensusString)
	if err != nil {
		return nil
	}

	if v != types.ConsensusClique {
		return errors.New("currently only Clique consensus is supported")
	}

	return nil
}

func validatePrivateTransactionManagerSelection(privateTransactionManagerInput string, nodeString string) error {
	privateTransactionManager, err := fftypes.FFEnumParseString(context.Background(), types.PrivateTransactionManager, privateTransactionManagerInput)
	if err != nil {
		return err
	}

	if !privateTransactionManager.Equals(types.PrivateTransactionManagerNone) {
		v, err := fftypes.FFEnumParseString(context.Background(), types.BlockchainNodeProvider, nodeString)
		if err != nil {
			return nil
		}

		if v != types.BlockchainNodeProviderQuorum {
			return errors.New("private transaction manager can only be enabled if blockchain node provider is Quorum")
		}
	}
	return nil
}

func validatePrivateTransactionManagerBlockchainConnectorCombination(privateTransactionManagerInput string, blockchainConnectorInput string) error {
	privateTransactionManager, err := fftypes.FFEnumParseString(context.Background(), types.PrivateTransactionManager, privateTransactionManagerInput)
	if err != nil {
		return err
	}

	blockchainConnector, err := fftypes.FFEnumParseString(context.Background(), types.BlockchainConnector, blockchainConnectorInput)
	if err != nil {
		return nil
	}

	if !privateTransactionManager.Equals(types.PrivateTransactionManagerNone) {
		if !blockchainConnector.Equals(types.BlockchainConnectorEthconnect) {
			return errors.New("currently only Ethconnect blockchain connector is supported with a private transaction manager")
		}
	}
	return nil
}

func validateTokensProvider(input []string, blockchainNodeProviderInput string) error {
	tokenProviders := make([]fftypes.FFEnum, len(input))
	for i, t := range input {
		tp, err := fftypes.FFEnumParseString(context.Background(), types.TokenProvider, t)
		if err != nil {
			return err
		}
		tokenProviders[i] = tp
	}

	nodeProvider, err := fftypes.FFEnumParseString(context.Background(), types.BlockchainNodeProvider, blockchainNodeProviderInput)
	if err != nil {
		return err
	}

	if nodeProvider.Equals(types.BlockchainNodeProviderRemoteRPC) {
		for _, t := range tokenProviders {
			if t.Equals(types.TokenProviderERC1155) {
				return errors.New("erc1155 is currently not supported with a remote-rpc node")
			}
		}
	}
	return nil
}

func validateReleaseChannel(input string) error {
	_, err := fftypes.FFEnumParseString(context.Background(), types.ReleaseChannelSelection, input)
	return err
}

func validateIPFSMode(input string) error {
	_, err := fftypes.FFEnumParseString(context.Background(), types.IPFSMode, input)
	return err
}

func randomHexString(length int) (string, error) {
	bytes := make([]byte, length)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}

func init() {
	initCmd.PersistentFlags().IntVarP(&initOptions.FireFlyBasePort, "firefly-base-port", "p", 5000, "Mapped port base of FireFly core API (1 added for each member)")
	initCmd.PersistentFlags().IntVarP(&initOptions.ServicesBasePort, "services-base-port", "s", 5100, "Mapped port base of services (100 added for each member)")
	initCmd.PersistentFlags().IntVar(&initOptions.PtmBasePort, "ptm-base-port", 4100, "Mapped port base of private transaction manager (10 added for each member)")
	initCmd.PersistentFlags().StringVarP(&initOptions.DatabaseProvider, "database", "d", "sqlite3", fmt.Sprintf("Database type to use. Options are: %v", fftypes.FFEnumValues(types.DatabaseSelection)))
	initCmd.Flags().StringVarP(&initOptions.BlockchainConnector, "blockchain-connector", "c", "evmconnect", fmt.Sprintf("Blockchain connector to use. Options are: %v", fftypes.FFEnumValues(types.BlockchainConnector)))
	initCmd.Flags().StringVarP(&initOptions.BlockchainProvider, "blockchain-provider", "b", "ethereum", fmt.Sprintf("Blockchain to use. Options are: %v", fftypes.FFEnumValues(types.BlockchainProvider)))
	initCmd.Flags().StringVarP(&initOptions.BlockchainNodeProvider, "blockchain-node", "n", "geth", fmt.Sprintf("Blockchain node type to use. Options are: %v", fftypes.FFEnumValues(types.BlockchainNodeProvider)))
	initCmd.PersistentFlags().StringVar(&initOptions.PrivateTransactionManager, "private-transaction-manager", "none", fmt.Sprintf("Private Transaction Manager to use. Options are: %v", fftypes.FFEnumValues(types.PrivateTransactionManager)))
	initCmd.PersistentFlags().StringVar(&initOptions.Consensus, "consensus", "clique", fmt.Sprintf("Consensus algorithm to use. Options are %v", fftypes.FFEnumValues(types.Consensus)))
	initCmd.PersistentFlags().StringArrayVarP(&initOptions.TokenProviders, "token-providers", "t", []string{"erc20_erc721"}, fmt.Sprintf("Token providers to use. Options are: %v", fftypes.FFEnumValues(types.TokenProvider)))
	initCmd.PersistentFlags().IntVarP(&initOptions.ExternalProcesses, "external", "e", 0, "Manage a number of FireFly core processes outside of the docker-compose stack - useful for development and debugging")
	initCmd.PersistentFlags().StringVarP(&initOptions.FireFlyVersion, "release", "r", "latest", fmt.Sprintf("Select the FireFly release version to use. Options are: %v", fftypes.FFEnumValues(types.ReleaseChannelSelection)))
	initCmd.PersistentFlags().StringVarP(&initOptions.ManifestPath, "manifest", "m", "", "Path to a manifest.json file containing the versions of each FireFly microservice to use. Overrides the --release flag.")
	initCmd.PersistentFlags().BoolVar(&promptNames, "prompt-names", false, "Prompt for org and node names instead of using the defaults")
	initCmd.PersistentFlags().BoolVar(&initOptions.PrometheusEnabled, "prometheus-enabled", false, "Enables Prometheus metrics exposition and aggregation to a shared Prometheus server")
	initCmd.PersistentFlags().BoolVar(&initOptions.SandboxEnabled, "sandbox-enabled", true, "Enables the FireFly Sandbox to be started with your FireFly stack")
	initCmd.PersistentFlags().IntVar(&initOptions.PrometheusPort, "prometheus-port", 9090, "Port for the shared Prometheus server")
	initCmd.PersistentFlags().StringVar(&initOptions.ExtraCoreConfigPath, "core-config", "", "The path to a yaml file containing extra config for FireFly Core")
	initCmd.PersistentFlags().StringVar(&initOptions.ExtraConnectorConfigPath, "connector-config", "", "The path to a yaml file containing extra config for the blockchain connector")
	initCmd.Flags().IntVar(&initOptions.BlockPeriod, "block-period", -1, "Block period in seconds. Default is variable based on selected blockchain provider.")
	initCmd.Flags().StringVar(&initOptions.ContractAddress, "contract-address", "", "Do not automatically deploy a contract, instead use a pre-configured address")
	initCmd.Flags().StringVar(&initOptions.RemoteNodeURL, "remote-node-url", "", "For cases where the node is pre-existing and running remotely")
	initCmd.Flags().Int64Var(&initOptions.ChainID, "chain-id", 2021, "The chain ID (Ethereum only) - also used as the network ID")
	initCmd.PersistentFlags().IntVar(&initOptions.RequestTimeout, "request-timeout", 0, "Custom request timeout (in seconds) - useful for registration to public chains")
	initCmd.PersistentFlags().StringVar(&initOptions.ReleaseChannel, "channel", "stable", fmt.Sprintf("Select the FireFly release channel to use. Options are: %v", fftypes.FFEnumValues(types.ReleaseChannelSelection)))
	initCmd.PersistentFlags().BoolVar(&initOptions.MultipartyEnabled, "multiparty", true, "Enable or disable multiparty mode")
	initCmd.PersistentFlags().StringVar(&initOptions.IPFSMode, "ipfs-mode", "private", fmt.Sprintf("Set the mode in which IFPS operates. Options are: %v", fftypes.FFEnumValues(types.IPFSMode)))
	initCmd.PersistentFlags().StringArrayVar(&initOptions.OrgNames, "org-name", []string{}, "Organization name")
	initCmd.PersistentFlags().StringArrayVar(&initOptions.NodeNames, "node-name", []string{}, "Node name")
	initCmd.PersistentFlags().BoolVar(&initOptions.RemoteNodeDeploy, "remote-node-deploy", false, "Enable or disable deployment of FireFly contracts on remote nodes")
	initCmd.PersistentFlags().StringToStringVar(&initOptions.EnvironmentVars, "environment-vars", map[string]string{}, "Common environment variables to set on all containers in FireFly stack") 
	initCmd.Flags().StringVar(&initOptions.FromBlock, "from-block", "0","For multiparty networks only: specify the starting block for event processing (default: 0)")
	rootCmd.AddCommand(initCmd)
}
