// Copyright © 2023 Kaleido, Inc.
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

package tezosconnect

import (
	"fmt"

	"github.com/hyperledger/firefly-cli/internal/docker"
	"github.com/hyperledger/firefly-cli/pkg/types"
)

func (t *Tezosconnect) GetServiceDefinitions(s *types.Stack, dependentServices map[string]string) []*docker.ServiceDefinition {
	dependsOn := make(map[string]map[string]string)
	for dep, state := range dependentServices {
		dependsOn[dep] = map[string]string{"condition": state}
	}
	serviceDefinitions := make([]*docker.ServiceDefinition, len(s.Members))
	for i, member := range s.Members {
		serviceDefinitions[i] = &docker.ServiceDefinition{
			ServiceName: "tezosconnect_" + member.ID,
			Service: &docker.Service{
				Image:         s.VersionManifest.Tezosconnect.GetDockerImageString(),
				ContainerName: fmt.Sprintf("%s_tezosconnect_%v", s.Name, i),
				Command:       "-f /tezosconnect/config/config.yaml",
				DependsOn:     dependsOn,
				Ports:         []string{fmt.Sprintf("%d:%v", member.ExposedConnectorPort, t.Port())},
				Volumes: []string{
					fmt.Sprintf("tezosconnect_config_%s:/tezosconnect/config", member.ID),
					fmt.Sprintf("tezosconnect_leveldb_%s:/tezosconnect/leveldb", member.ID),
				},
				Logging: docker.StandardLogOptions,
			},
			VolumeNames: []string{
				fmt.Sprintf("tezosconnect_config_%s", member.ID),
				fmt.Sprintf("tezosconnect_leveldb_%s", member.ID),
			},
		}
	}
	return serviceDefinitions
}
