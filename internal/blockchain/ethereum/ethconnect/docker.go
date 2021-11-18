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

	"github.com/hyperledger/firefly-cli/internal/docker"
	"github.com/hyperledger/firefly-cli/pkg/types"
)

func GetEthconnectServiceDefinitions(s *types.Stack, blockchainServiceName string) []*docker.ServiceDefinition {
	serviceDefinitions := make([]*docker.ServiceDefinition, len(s.Members))
	for i, member := range s.Members {
		serviceDefinitions[i] = &docker.ServiceDefinition{
			ServiceName: "ethconnect_" + member.ID,
			Service: &docker.Service{
				Image:         s.VersionManifest.Ethconnect.GetDockerImageString(),
				ContainerName: fmt.Sprintf("%s_ethconnect_%v", s.Name, i),
				Command:       "server -f ./config/config.yaml -d 2",
				DependsOn:     map[string]map[string]string{blockchainServiceName: {"condition": "service_started"}},
				Ports:         []string{fmt.Sprintf("%d:8080", member.ExposedConnectorPort)},
				Volumes: []string{
					fmt.Sprintf("ethconnect_config_%s:/ethconnect/config", member.ID),
					fmt.Sprintf("ethconnect_abis_%s:/ethconnect/abis", member.ID),
					fmt.Sprintf("ethconnect_events_%s:/ethconnect/events", member.ID),
				},
				Logging: docker.StandardLogOptions,
			},
			VolumeNames: []string{
				fmt.Sprintf("ethconnect_config_%v", member.ID),
				fmt.Sprintf("ethconnect_abis_%v", member.ID),
				fmt.Sprintf("ethconnect_events_%v", member.ID),
			},
		}
	}
	return serviceDefinitions
}
