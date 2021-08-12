package stacks

import (
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/briandowns/spinner"
	secp256k1 "github.com/btcsuite/btcd/btcec"
	"github.com/hyperledger-labs/firefly-cli/internal/contracts"
	"github.com/hyperledger-labs/firefly-cli/internal/docker"
	"github.com/hyperledger-labs/firefly-cli/internal/geth"
	"golang.org/x/crypto/sha3"

	"gopkg.in/yaml.v2"
)

var homeDir, _ = os.UserHomeDir()
var StacksDir = filepath.Join(homeDir, ".firefly", "stacks")

type DatabaseSelection int

const (
	PostgreSQL DatabaseSelection = iota
	SQLite3
)

var DBSelectionStrings = []string{"postgres", "sqlite3"}

func (db DatabaseSelection) String() string {
	return DBSelectionStrings[db]
}

func DatabaseSelectionFromString(s string) (DatabaseSelection, error) {
	for i, dbSelection := range DBSelectionStrings {
		if strings.ToLower(s) == dbSelection {
			return DatabaseSelection(i), nil
		}
	}
	return SQLite3, fmt.Errorf("\"%s\" is not a valid database selection. valid options are: %v", s, DBSelectionStrings)
}

type Stack struct {
	Name            string    `json:"name,omitempty"`
	Members         []*Member `json:"members,omitempty"`
	SwarmKey        string    `json:"swarmKey,omitempty"`
	ExposedGethPort int       `json:"exposedGethPort,omitempty"`
	Database        string    `json:"database"`
}

type Member struct {
	ID                      string `json:"id,omitempty"`
	Index                   *int   `json:"index,omitempty"`
	Address                 string `json:"address,omitempty"`
	PrivateKey              string `json:"privateKey,omitempty"`
	ExposedFireflyPort      int    `json:"exposedFireflyPort,omitempty"`
	ExposedFireflyAdminPort int    `json:"exposedFireflyAdminPort,omitempty"`
	ExposedEthconnectPort   int    `json:"exposedEthconnectPort,omitempty"`
	ExposedPostgresPort     int    `json:"exposedPostgresPort,omitempty"`
	ExposedDataexchangePort int    `json:"exposedDataexchangePort,omitempty"`
	ExposedIPFSApiPort      int    `json:"exposedIPFSApiPort,omitempty"`
	ExposedIPFSGWPort       int    `json:"exposedIPFSGWPort,omitempty"`
	ExposedUIPort           int    `json:"exposedUiPort,omitempty"`
	External                bool   `json:"external,omitempty"`
}

type StartOptions struct {
	NoPull     bool
	NoRollback bool
}

type InitOptions struct {
	FireFlyBasePort   int
	ServicesBasePort  int
	DatabaseSelection string
	Verbose           bool
	ExternalProcesses int
}

func ListStacks() ([]string, error) {
	files, err := ioutil.ReadDir(StacksDir)
	if err != nil {
		return nil, err
	}

	stacks := make([]string, 0)
	i := 0
	for _, f := range files {
		if f.IsDir() {
			if exists, err := CheckExists(f.Name()); err == nil && exists {
				stacks = append(stacks, f.Name())
				i++
			}
		}
	}
	return stacks, nil
}

func InitStack(stackName string, memberCount int, options *InitOptions) error {

	dbSelection, err := DatabaseSelectionFromString(options.DatabaseSelection)
	if err != nil {
		return err
	}

	stack := &Stack{
		Name:            stackName,
		Members:         make([]*Member, memberCount),
		SwarmKey:        GenerateSwarmKey(),
		ExposedGethPort: options.ServicesBasePort,
		Database:        dbSelection.String(),
	}

	for i := 0; i < memberCount; i++ {
		externalProcess := i < options.ExternalProcesses
		stack.Members[i] = createMember(fmt.Sprint(i), i, options, externalProcess)
	}
	compose := CreateDockerCompose(stack)
	if err := stack.ensureDirectories(); err != nil {
		return err
	}
	if err := stack.writeDockerCompose(compose); err != nil {
		return fmt.Errorf("failed to write docker-compose.yml: %s", err)
	}
	return stack.writeConfigs(options.Verbose)
}

func CheckExists(stackName string) (bool, error) {
	_, err := os.Stat(filepath.Join(StacksDir, stackName, "stack.json"))
	if os.IsNotExist(err) {
		return false, nil
	} else if err != nil {
		return false, err
	} else {
		return true, nil
	}
}

func LoadStack(stackName string) (*Stack, error) {
	exists, err := CheckExists(stackName)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, fmt.Errorf("stack '%s' does not exist", stackName)
	}
	fmt.Printf("reading stack config... ")
	if d, err := ioutil.ReadFile(filepath.Join(StacksDir, stackName, "stack.json")); err != nil {
		return nil, err
	} else {
		var stack *Stack
		if err := json.Unmarshal(d, &stack); err == nil {
			fmt.Printf("done\n")
		}
		return stack, err
	}

}

func (s *Stack) ensureDirectories() error {

	stackDir := filepath.Join(StacksDir, s.Name)
	dataDir := filepath.Join(stackDir, "data")

	if err := os.MkdirAll(filepath.Join(stackDir, "configs"), 0755); err != nil {
		return err
	}

	for _, member := range s.Members {
		if err := os.MkdirAll(filepath.Join(dataDir, "dataexchange_"+member.ID, "peer-certs"), 0755); err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Join(stackDir, "geth", member.ID), 0755); err != nil {
			return err
		}
		if member.External && s.Database == "sqlite3" {
			if err := os.MkdirAll(filepath.Join(dataDir, "sqlite"), 0755); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Stack) writeDockerCompose(compose *DockerComposeConfig) error {
	bytes, err := yaml.Marshal(compose)
	if err != nil {
		return err
	}

	stackDir := filepath.Join(StacksDir, s.Name)

	return ioutil.WriteFile(filepath.Join(stackDir, "docker-compose.yml"), bytes, 0755)
}

func (s *Stack) writeConfigs(verbose bool) error {
	stackDir := filepath.Join(StacksDir, s.Name)

	fireflyConfigs := NewFireflyConfigs(s)
	for memberId, config := range fireflyConfigs {
		if err := WriteFireflyConfig(config, filepath.Join(stackDir, "configs", fmt.Sprintf("firefly_core_%s.yml", memberId))); err != nil {
			return err
		}
	}

	stackConfigBytes, _ := json.MarshalIndent(s, "", " ")
	if err := ioutil.WriteFile(filepath.Join(stackDir, "stack.json"), stackConfigBytes, 0755); err != nil {
		return err
	}

	if err := ioutil.WriteFile(filepath.Join(stackDir, "geth", "password"), []byte("correcthorsebatterystaple"), 0755); err != nil {
		return err
	}

	for _, member := range s.Members {
		// Drop the 0x on the front of the private key here because that's what geth is expecting in the keyfile
		if err := ioutil.WriteFile(filepath.Join(stackDir, "geth", member.ID, "keyfile"), []byte(member.PrivateKey[2:]), 0755); err != nil {
			return err
		}
	}

	return s.writeGenesisJson(verbose)
}

func (s *Stack) initializeGethNode(verbose bool) error {

	volumeName := fmt.Sprintf("%s_geth", s.Name)
	gethConfigDir := path.Join(StacksDir, s.Name, "geth")

	for _, member := range s.Members {
		// TODO: Revisit this when member names are customizable. I doubt this will work if they have spaces in them
		if err := docker.RunDockerCommand(StacksDir, verbose, verbose, "run", "--rm", "-v", fmt.Sprintf("%s:/geth", gethConfigDir), "-v", fmt.Sprintf("%s:/data", volumeName), "ethereum/client-go:release-1.9", "--nousb", "account", "import", "--password", "/geth/password", "--keystore", "/data/keystore", fmt.Sprintf("/geth/%s/keyfile", member.ID)); err != nil {
			return err
		}
	}
	if err := docker.CopyFileToVolume(volumeName, path.Join(gethConfigDir, "genesis.json"), "genesis.json", verbose); err != nil {
		return err
	}
	if err := docker.CopyFileToVolume(volumeName, path.Join(gethConfigDir, "password"), "password", verbose); err != nil {
		return err
	}

	if err := docker.RunDockerCommand(StacksDir, verbose, verbose, "run", "--rm", "-v", fmt.Sprintf("%s:/data", volumeName), "ethereum/client-go:release-1.9", "--datadir", "/data", "--nousb", "init", "/data/genesis.json"); err != nil {
		return err
	}

	return nil
}

func (s *Stack) writeGenesisJson(verbose bool) error {
	stackDir := filepath.Join(StacksDir, s.Name)

	addresses := make([]string, len(s.Members))
	for i, member := range s.Members {
		// Drop the 0x on the front of the address here because that's what geth is expecting in the genesis.json
		addresses[i] = member.Address[2:]
	}
	genesis := geth.CreateGenesisJson(addresses)
	genesisJsonBytes, _ := json.MarshalIndent(genesis, "", " ")
	if err := ioutil.WriteFile(filepath.Join(stackDir, "geth", "genesis.json"), genesisJsonBytes, 0755); err != nil {
		return err
	}
	return nil
}

func (s *Stack) writeDataExchangeCerts(verbose bool) error {
	stackDir := filepath.Join(StacksDir, s.Name)
	for _, member := range s.Members {

		memberDXDir := path.Join(stackDir, "data", "dataexchange_"+member.ID)

		// TODO: remove dependency on openssl here
		opensslCmd := exec.Command("openssl", "req", "-new", "-x509", "-nodes", "-days", "365", "-subj", fmt.Sprintf("/CN=dataexchange_%s/O=member_%s", member.ID, member.ID), "-keyout", "key.pem", "-out", "cert.pem")
		opensslCmd.Dir = filepath.Join(stackDir, "data", "dataexchange_"+member.ID)
		if err := opensslCmd.Run(); err != nil {
			return err
		}

		dataExchangeConfig := s.GenerateDataExchangeHTTPSConfig(member.ID)
		configBytes, err := json.Marshal(dataExchangeConfig)
		if err != nil {
			log.Fatal(err)
		}
		ioutil.WriteFile(path.Join(memberDXDir, "config.json"), configBytes, 0755)

		// Copy files into docker volumes
		volumeName := fmt.Sprintf("%s_dataexchange_%s", s.Name, member.ID)
		docker.MkdirInVolume(volumeName, "peer-certs", verbose)
		docker.CopyFileToVolume(volumeName, path.Join(memberDXDir, "config.json"), "/config.json", verbose)
		docker.CopyFileToVolume(volumeName, path.Join(memberDXDir, "cert.pem"), "/cert.pem", verbose)
		docker.CopyFileToVolume(volumeName, path.Join(memberDXDir, "key.pem"), "/key.pem", verbose)
	}
	return nil
}

func createMember(id string, index int, options *InitOptions, external bool) *Member {
	privateKey, _ := secp256k1.NewPrivateKey(secp256k1.S256())
	privateKeyBytes := privateKey.Serialize()
	encodedPrivateKey := "0x" + hex.EncodeToString(privateKeyBytes)
	// Remove the "04" Suffix byte when computing the address. This byte indicates that it is an uncompressed public key.
	publicKeyBytes := privateKey.PubKey().SerializeUncompressed()[1:]
	// Take the hash of the public key to generate the address
	hash := sha3.NewLegacyKeccak256()
	hash.Write(publicKeyBytes)
	// Ethereum addresses only use the lower 20 bytes, so toss the rest away
	encodedAddress := "0x" + hex.EncodeToString(hash.Sum(nil)[12:32])

	serviceBase := options.ServicesBasePort + (index * 100)
	return &Member{
		ID:                      id,
		Index:                   &index,
		Address:                 encodedAddress,
		PrivateKey:              encodedPrivateKey,
		ExposedFireflyPort:      options.FireFlyBasePort + index,
		ExposedFireflyAdminPort: serviceBase + 1, // note shared geth is on zero
		ExposedEthconnectPort:   serviceBase + 2,
		ExposedUIPort:           serviceBase + 3,
		ExposedPostgresPort:     serviceBase + 4,
		ExposedDataexchangePort: serviceBase + 5,
		ExposedIPFSApiPort:      serviceBase + 6,
		ExposedIPFSGWPort:       serviceBase + 7,
		External:                external,
	}
}

func updateStatus(message string, spin *spinner.Spinner) {
	if spin != nil {
		spin.Suffix = fmt.Sprintf(" %s...", message)
	} else {
		fmt.Println(message)
	}
}

func (s *Stack) StartStack(fancyFeatures bool, verbose bool, options *StartOptions) error {
	fmt.Printf("starting FireFly stack '%s'... ", s.Name)
	// Check to make sure all of our ports are available
	if err := s.checkPortsAvailable(); err != nil {
		return err
	}
	workingDir := filepath.Join(StacksDir, s.Name)
	var spin *spinner.Spinner
	if fancyFeatures && !verbose {
		spin = spinner.New(spinner.CharSets[11], 100*time.Millisecond)
		spin.FinalMSG = "done"
	}
	if hasBeenRun, err := s.StackHasRunBefore(); !hasBeenRun && err == nil {
		fmt.Println("\nthis will take a few seconds longer since this is the first time you're running this stack...")
		if spin != nil {
			spin.Start()
		}
		if err := s.runFirstTimeSetup(spin, verbose, options); err != nil {
			// Something bad happened during setup
			if options.NoRollback {
				return err
			} else {
				// Rollback changes
				updateStatus("an error occurred - rolling back changes", spin)
				resetErr := s.ResetStack(verbose)
				if spin != nil {
					spin.Stop()
				}

				var finalErr error

				if resetErr != nil {
					finalErr = fmt.Errorf("%s - error resetting stack: %s", err.Error(), resetErr.Error())
				} else {
					finalErr = fmt.Errorf("%s - all changes rolled back", err.Error())
				}

				return finalErr
			}
		}
		if spin != nil {
			spin.Stop()
		}
		return nil
	} else if err == nil {
		if spin != nil {
			spin.Start()
		}
		updateStatus("starting FireFly dependencies", spin)
		if err := docker.RunDockerComposeCommand(workingDir, verbose, verbose, "up", "-d"); err != nil {
			return err
		}
		err := s.UnlockAccounts(spin)
		s.ensureFireflyNodesUp(false, spin)

		if spin != nil {
			spin.Stop()
		}
		return err
	} else {
		if spin != nil {
			spin.Stop()
		}
		return err
	}
}

func (s *Stack) StopStack(verbose bool) error {
	return docker.RunDockerComposeCommand(filepath.Join(StacksDir, s.Name), verbose, verbose, "stop")
}

func (s *Stack) ResetStack(verbose bool) error {
	if err := docker.RunDockerComposeCommand(filepath.Join(StacksDir, s.Name), verbose, verbose, "down", "--volumes"); err != nil {
		return err
	}
	if err := os.RemoveAll(filepath.Join(StacksDir, s.Name, "data")); err != nil {
		return err
	}
	return s.ensureDirectories()
}

func (s *Stack) RemoveStack(verbose bool) error {
	if err := s.ResetStack(verbose); err != nil {
		return err
	}
	return os.RemoveAll(filepath.Join(StacksDir, s.Name))
}

func (s *Stack) checkPortsAvailable() error {
	ports := make([]int, 1)
	ports[0] = s.ExposedGethPort
	for _, member := range s.Members {
		ports = append(ports, member.ExposedDataexchangePort)
		ports = append(ports, member.ExposedEthconnectPort)
		if !member.External {
			ports = append(ports, member.ExposedFireflyAdminPort)
			ports = append(ports, member.ExposedFireflyPort)
		}
		ports = append(ports, member.ExposedIPFSApiPort)
		ports = append(ports, member.ExposedIPFSGWPort)
		ports = append(ports, member.ExposedPostgresPort)
		ports = append(ports, member.ExposedUIPort)
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

func (s *Stack) runFirstTimeSetup(spin *spinner.Spinner, verbose bool, options *StartOptions) error {
	workingDir := filepath.Join(StacksDir, s.Name)

	updateStatus("initializing geth node", spin)
	if err := s.initializeGethNode(verbose); err != nil {
		return err
	}

	updateStatus("writing data exchange certs", spin)
	if err := s.writeDataExchangeCerts(verbose); err != nil {
		return err
	}

	// write firefly configs to volumes
	for _, member := range s.Members {
		if !member.External {
			updateStatus(fmt.Sprintf("copying firefly.core to firefly_core_%s", member.ID), spin)
			volumeName := fmt.Sprintf("%s_firefly_core_%s", s.Name, member.ID)
			if err := docker.CopyFileToVolume(volumeName, path.Join(workingDir, "configs", fmt.Sprintf("firefly_core_%s.yml", member.ID)), "/firefly.core", verbose); err != nil {
				return err
			}
		}
	}

	if !options.NoPull {
		updateStatus("pulling latest versions", spin)
		if err := docker.RunDockerComposeCommand(workingDir, verbose, verbose, "pull"); err != nil {
			return err
		}
	}

	updateStatus("starting FireFly dependencies", spin)
	if err := docker.RunDockerComposeCommand(workingDir, verbose, verbose, "up", "-d"); err != nil {
		return err
	}

	if err := s.UnlockAccounts(spin); err != nil {
		return err
	}

	var containerName string
	for _, member := range s.Members {
		if !member.External {
			containerName = fmt.Sprintf("%s_firefly_core_%s_1", s.Name, member.ID)
			break
		}
	}
	if containerName == "" {
		return errors.New("unable to extract contracts from container - no valid firefly core containers found in stack")
	}
	updateStatus("extracting smart contracts", spin)
	if err := s.extractContracts(containerName, verbose); err != nil {
		return err
	}

	if err := s.ensureFireflyNodesUp(true, spin); err != nil {
		return err
	}

	updateStatus("deploying smart contracts", spin)
	if err := s.deployContracts(spin, verbose); err != nil {
		return err
	}
	updateStatus("registering FireFly identities", spin)
	if err := s.registerFireflyIdentities(spin, verbose); err != nil {
		return err
	}
	return nil
}

func (s *Stack) ensureFireflyNodesUp(firstTimeSetup bool, spin *spinner.Spinner) error {
	for _, member := range s.Members {
		if member.External {
			configFilename := path.Join(StacksDir, s.Name, "configs", fmt.Sprintf("firefly_core_%v.yml", member.ID))
			var port int
			if firstTimeSetup {
				port = member.ExposedFireflyAdminPort
			} else {
				port = member.ExposedFireflyPort
			}
			// Check process running
			available, err := checkPortAvailable(port)
			if err != nil {
				return err
			}
			if available {
				updateStatus(fmt.Sprintf("please start your firefly core with the config file for this stack: firefly -f %s  ", configFilename), spin)
				if err := s.waitForFireflyStart(port); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func (s *Stack) waitForFireflyStart(port int) error {
	retries := 120
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

func (s *Stack) UpgradeStack(verbose bool) error {
	workingDir := filepath.Join(StacksDir, s.Name)
	if err := docker.RunDockerComposeCommand(workingDir, verbose, verbose, "down"); err != nil {
		return err
	}
	return docker.RunDockerComposeCommand(workingDir, verbose, verbose, "pull")
}

func (s *Stack) PrintStackInfo(verbose bool) error {
	workingDir := filepath.Join(StacksDir, s.Name)
	fmt.Print("\n")
	if err := docker.RunDockerComposeCommand(workingDir, verbose, true, "images"); err != nil {
		return err
	}
	fmt.Print("\n")
	if err := docker.RunDockerComposeCommand(workingDir, verbose, true, "ps"); err != nil {
		return err
	}
	fmt.Printf("\nYour docker compose file for this stack can be found at: %s\n\n", filepath.Join(StacksDir, s.Name, "docker-compose.yml"))
	return nil
}

func (s *Stack) deployContract(member *Member, contract *contracts.Contract, name string, args map[string]string) (string, error) {
	ethconnectUrl := fmt.Sprintf("http://127.0.0.1:%v", member.ExposedEthconnectPort)
	abiResponse, err := contracts.PublishABI(ethconnectUrl, contract)
	if err != nil {
		return "", err
	}
	deployResponse, err := contracts.DeployContract(ethconnectUrl, abiResponse.ID, member.Address, args, name)
	if err != nil {
		return "", err
	}
	return deployResponse.ContractAddress, nil
}

func (s *Stack) registerContract(member *Member, contract *contracts.Contract, contractAddress string, name string, args map[string]string) error {
	ethconnectUrl := fmt.Sprintf("http://127.0.0.1:%v", member.ExposedEthconnectPort)
	abiResponse, err := contracts.PublishABI(ethconnectUrl, contract)
	if err != nil {
		return err
	}
	_, err = contracts.RegisterContract(ethconnectUrl, abiResponse.ID, contractAddress, member.Address, name, args)
	if err != nil {
		return err
	}
	return nil
}

func (s *Stack) deployContracts(spin *spinner.Spinner, verbose bool) error {
	fireflyContract, err := contracts.ReadCompiledContract(filepath.Join(StacksDir, s.Name, "contracts", "Firefly.json"))
	if err != nil {
		return err
	}

	var fireflyContractAddress string
	for _, member := range s.Members {
		if fireflyContractAddress == "" {
			// TODO: version the registered name
			updateStatus(fmt.Sprintf("deploying firefly contract on '%s'", member.ID), spin)
			fireflyContractAddress, err = s.deployContract(member, fireflyContract, "firefly", map[string]string{})
			if err != nil {
				return err
			}
		} else {
			updateStatus(fmt.Sprintf("registering firefly contract on '%s'", member.ID), spin)
			err = s.registerContract(member, fireflyContract, fireflyContractAddress, "firefly", map[string]string{})
			if err != nil {
				return err
			}
		}
	}

	if err := s.patchConfigAndRestartFireflyNodes(verbose, spin); err != nil {
		return err
	}

	return nil
}

func (s *Stack) patchConfigAndRestartFireflyNodes(verbose bool, spin *spinner.Spinner) error {
	for _, member := range s.Members {
		updateStatus(fmt.Sprintf("applying configuration changes to %s", member.ID), spin)
		configRecordUrl := fmt.Sprintf("http://localhost:%d/admin/api/v1/config/records/admin", member.ExposedFireflyAdminPort)
		if err := s.httpJSONWithRetry("PUT", configRecordUrl, "{\"preInit\": false}", nil); err != nil && err != io.EOF {
			return err
		}
		resetUrl := fmt.Sprintf("http://localhost:%d/admin/api/v1/config/reset", member.ExposedFireflyAdminPort)
		if err := s.httpJSONWithRetry("POST", resetUrl, "{}", nil); err != nil {
			return err
		}
	}
	return nil
}

func (s *Stack) extractContracts(containerName string, verbose bool) error {
	workingDir := filepath.Join(StacksDir, s.Name)
	destinationDir := filepath.Join(workingDir, "contracts")
	if err := docker.RunDockerCommand(workingDir, verbose, verbose, "cp", containerName+":/firefly/contracts", destinationDir); err != nil {
		return err
	}
	return nil
}

func (s *Stack) StackHasRunBefore() (bool, error) {
	path := filepath.Join(StacksDir, s.Name, "data", fmt.Sprintf("dataexchange_%s", s.Members[0].ID), "cert.pem")
	_, err := os.Stat(path)
	if os.IsNotExist(err) {
		return false, nil
	} else if err != nil {
		return false, err
	} else {
		return true, nil
	}
}

func (s *Stack) UnlockAccounts(spin *spinner.Spinner) error {
	gethClient := geth.NewGethClient(fmt.Sprintf("http://127.0.0.1:%v", s.ExposedGethPort))
	for _, m := range s.Members {
		retries := 10
		updateStatus(fmt.Sprintf("unlocking account for member %s", m.ID), spin)
		for {
			if err := gethClient.UnlockAccount(m.Address, "correcthorsebatterystaple"); err != nil {
				if retries == 0 {
					return fmt.Errorf("unable to unlock account %s for member %s", m.Address, m.ID)
				}
				time.Sleep(time.Second * 1)
				retries--
			} else {
				break
			}
		}
	}
	return nil
}
