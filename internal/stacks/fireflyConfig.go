package stacks

import (
	"fmt"
	"io/ioutil"
	"path"

	"gopkg.in/yaml.v2"
)

type LogConfig struct {
	Level string `yaml:"level,omitempty"`
}

type HttpServerConfig struct {
	Port      int    `yaml:"port,omitempty"`
	Address   string `yaml:"address,omitempty"`
	PublicURL string `yaml:"publicURL,omitempty"`
}

type AdminServerConfig struct {
	Port      int    `yaml:"port,omitempty"`
	Address   string `yaml:"address,omitempty"`
	Enabled   bool   `yaml:"enabled,omitempty"`
	PreInit   bool   `yaml:"preinit,omitempty"`
	PublicURL string `yaml:"publicURL,omitempty"`
}

type BasicAuth struct {
	Username string `yaml:"username,omitempty"`
	Password string `yaml:"password,omitempty"`
}

type HttpEndpointConfig struct {
	URL  string    `yaml:"url,omitempty"`
	Auth BasicAuth `yaml:"auth,omitempty"`
}

type UIConfig struct {
	Path string `yaml:"path,omitempty"`
}

type NodeConfig struct {
	Name string `yaml:"name,omitempty"`
}

type OrgConfig struct {
	Name     string `yaml:"name,omitempty"`
	Identity string `yaml:"identity,omitempty"`
}

type EthconnectConfig struct {
	URL                 string     `yaml:"url,omitempty"`
	Instance            string     `yaml:"instance,omitempty"`
	Topic               string     `yaml:"topic,omitempty"`
	SkipEventStreamInit bool       `yaml:"skipEventstreamInit,omitempty"`
	Auth                *BasicAuth `yaml:"auth,omitempty"`
}

type EthereumConfig struct {
	Ethconnect *EthconnectConfig `yaml:"ethconnect,omitempty"`
}

type BlockchainConfig struct {
	Type     string          `yaml:"type,omitempty"`
	Ethereum *EthereumConfig `yaml:"ethereum,omitempty"`
}

type DataExchangeConfig struct {
	Type  string              `yaml:"type,omitempty"`
	HTTPS *HttpEndpointConfig `yaml:"https,omitempty"`
}

type CommonDBConfig struct {
	URL        string            `yaml:"url,omitempty"`
	Migrations *MigrationsConfig `yaml:"migrations,omitempty"`
}

type MigrationsConfig struct {
	Auto      bool   `yaml:"auto,omitempty"`
	Directory string `yaml:"directory,omitempty"`
}

type DatabaseConfig struct {
	Type       string          `yaml:"type,omitempty"`
	PostgreSQL *CommonDBConfig `yaml:"postgres,omitempty"`
	SQLite3    *CommonDBConfig `yaml:"sqlite3,omitempty"`
}

type PublicStorageConfig struct {
	Type string             `yaml:"type,omitempty"`
	IPFS *FireflyIPFSConfig `yaml:"ipfs,omitempty"`
}

type FireflyIPFSConfig struct {
	API     *HttpEndpointConfig `yaml:"api,omitempty"`
	Gateway *HttpEndpointConfig `yaml:"gateway,omitempty"`
}

type FireflyConfig struct {
	Log          *LogConfig           `yaml:"log,omitempty"`
	Debug        *HttpServerConfig    `yaml:"debug,omitempty"`
	HTTP         *HttpServerConfig    `yaml:"http,omitempty"`
	Admin        *AdminServerConfig   `yaml:"admin,omitempty"`
	UI           *UIConfig            `yaml:"ui,omitempty"`
	Node         *NodeConfig          `yaml:"node,omitempty"`
	Org          *OrgConfig           `yaml:"org,omitempty"`
	Blockchain   *BlockchainConfig    `yaml:"blockchain,omitempty"`
	Database     *DatabaseConfig      `yaml:"database,omitempty"`
	P2PFS        *PublicStorageConfig `yaml:"publicstorage,omitempty"`
	DataExchange *DataExchangeConfig  `yaml:"dataexchange,omitempty"`
}

func NewFireflyConfigs(stack *Stack) map[string]*FireflyConfig {
	configs := make(map[string]*FireflyConfig)

	for _, member := range stack.Members {
		memberConfig := &FireflyConfig{
			Log: &LogConfig{
				Level: "debug",
			},
			Debug: &HttpServerConfig{
				Port: 6060,
			},
			HTTP: &HttpServerConfig{
				Port:      member.ExposedFireflyPort,
				Address:   "0.0.0.0",
				PublicURL: fmt.Sprintf("http://127.0.0.1:%d", member.ExposedFireflyPort),
			},
			Admin: &AdminServerConfig{
				Enabled:   true,
				Port:      member.ExposedFireflyAdminPort,
				Address:   "0.0.0.0",
				PreInit:   true,
				PublicURL: fmt.Sprintf("http://127.0.0.1:%d", member.ExposedFireflyAdminPort),
			},
			UI: &UIConfig{
				Path: "./frontend",
			},
			Node: &NodeConfig{
				Name: fmt.Sprintf("node_%s", member.ID),
			},
			Org: &OrgConfig{
				Name:     fmt.Sprintf("org_%s", member.ID),
				Identity: member.Address,
			},
			Blockchain: &BlockchainConfig{
				Type: "ethereum",
				Ethereum: &EthereumConfig{
					Ethconnect: &EthconnectConfig{
						URL:      getEthconnectURL(member),
						Instance: "/contracts/firefly",
						Topic:    member.ID,
					},
				},
			},
			P2PFS: &PublicStorageConfig{
				Type: "ipfs",
				IPFS: &FireflyIPFSConfig{
					API: &HttpEndpointConfig{
						URL: getIPFSAPIURL(member),
					},
					Gateway: &HttpEndpointConfig{
						URL: getIPFSGatewayURL(member),
					},
				},
			},
			DataExchange: &DataExchangeConfig{
				HTTPS: &HttpEndpointConfig{
					URL: getDataExchangeURL(member),
				},
			},
		}
		switch stack.Database {
		case "postgres":
			memberConfig.Database = &DatabaseConfig{
				Type: "postgres",
				PostgreSQL: &CommonDBConfig{
					URL: getPostgresURL(member),
					Migrations: &MigrationsConfig{
						Auto: true,
					},
				},
			}
		case "sqlite3":
			memberConfig.Database = &DatabaseConfig{
				Type: stack.Database,
				SQLite3: &CommonDBConfig{
					URL: getSQLitePath(member, stack.Name),
					Migrations: &MigrationsConfig{
						Auto: true,
					},
				},
			}
		}
		configs[member.ID] = memberConfig
	}
	return configs
}

func getEthconnectURL(member *Member) string {
	if !member.External {
		return fmt.Sprintf("http://ethconnect_%s:8080", member.ID)
	} else {
		return fmt.Sprintf("http://127.0.0.1:%v", member.ExposedEthconnectPort)
	}
}

func getIPFSAPIURL(member *Member) string {
	if !member.External {
		return fmt.Sprintf("http://ipfs_%s:5001", member.ID)
	} else {
		return fmt.Sprintf("http://127.0.0.1:%v", member.ExposedIPFSApiPort)
	}
}

func getIPFSGatewayURL(member *Member) string {
	if !member.External {
		return fmt.Sprintf("http://ipfs_%s:8080", member.ID)
	} else {
		return fmt.Sprintf("http://127.0.0.1:%v", member.ExposedIPFSGWPort)
	}
}

func getPostgresURL(member *Member) string {
	if !member.External {
		return fmt.Sprintf("postgres://postgres:f1refly@postgres_%s:5432?sslmode=disable", member.ID)
	} else {
		return fmt.Sprintf("postgres://postgres:f1refly@127.0.0.1:%v?sslmode=disable", member.ExposedPostgresPort)
	}
}

func getSQLitePath(member *Member, stackName string) string {
	if !member.External {
		return "/etc/firefly/db?_busy_timeout=5000"
	} else {
		return path.Join(StacksDir, stackName, "data", "sqlite", member.ID+".db")
	}
}

func getDataExchangeURL(member *Member) string {
	if !member.External {
		return fmt.Sprintf("http://dataexchange_%s:3000", member.ID)
	} else {
		return fmt.Sprintf("http://127.0.0.1:%v", member.ExposedDataexchangePort)
	}
}

func ReadFireflyConfig(filePath string) (*FireflyConfig, error) {
	if bytes, err := ioutil.ReadFile(filePath); err != nil {
		return nil, err
	} else {
		var config *FireflyConfig
		err := yaml.Unmarshal(bytes, &config)
		return config, err
	}
}

func WriteFireflyConfig(config *FireflyConfig, filePath string) error {
	if bytes, err := yaml.Marshal(config); err != nil {
		return err
	} else {
		return ioutil.WriteFile(filePath, bytes, 0755)
	}
}
