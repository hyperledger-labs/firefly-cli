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

package ethconnect

import (
	"fmt"
	"io/ioutil"
	"path/filepath"

	"github.com/hyperledger/firefly-cli/pkg/types"
	"gopkg.in/yaml.v2"
)

type Config struct {
	Rest *Rest `yaml:"rest,omitempty"`
}

type Rest struct {
	RestGateway *RestGateway `yaml:"rest-gateway,omitempty"`
}

type RestGateway struct {
	RPC           *RPC     `yaml:"rpc,omitempty"`
	OpenAPI       *OpenAPI `yaml:"openapi,omitempty"`
	HTTP          *HTTP    `yaml:"http,omitempty"`
	MaxTXWaitTime int      `yaml:"maxTXWaitTime,omitempty"`
	MaxInFlight   int      `yaml:"maxInFlight,omitempty"`
}

type RPC struct {
	URL string `yaml:"url,omitempty"`
}

type OpenAPI struct {
	EventPollingIntervalSec int    `yaml:"eventPollingIntervalSec,omitempty"`
	StoragePath             string `yaml:"storagePath,omitempty"`
	EventsDB                string `yaml:"eventsDB,omitempty"`
}

type HTTP struct {
	Port int `yaml:"port,omitempty"`
}

func (e *Config) WriteConfig(filename string) error {
	configYamlBytes, _ := yaml.Marshal(e)
	if err := ioutil.WriteFile(filepath.Join(filename), configYamlBytes, 0755); err != nil {
		return err
	}
	return nil
}

func GenerateEthconnectConfig(member *types.Member, blockchainServiceName string) *Config {
	return &Config{
		Rest: &Rest{
			RestGateway: &RestGateway{
				MaxTXWaitTime: 60,
				MaxInFlight:   10,
				RPC:           &RPC{URL: fmt.Sprintf("http://%s:8545", blockchainServiceName)},
				OpenAPI: &OpenAPI{
					EventPollingIntervalSec: 1,
					StoragePath:             "./abis",
					EventsDB:                "./events",
				},
				HTTP: &HTTP{
					Port: 8080,
				},
			},
		},
	}
}
