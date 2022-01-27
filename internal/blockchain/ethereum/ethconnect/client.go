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
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"mime/multipart"
	"net/http"
	"net/url"
	"path"
	"strings"

	"github.com/hyperledger/firefly-cli/pkg/types"
)

type PublishAbiResponseBody struct {
	ID string `json:"id,omitempty"`
}

type DeployContractResponseBody struct {
	ContractAddress string `json:"contractAddress,omitempty"`
}

type RegisterResponseBody struct {
	Created      string `json:"created,omitempty"`
	Address      string `json:"string,omitempty"`
	Path         string `json:"path,omitempty"`
	ABI          string `json:"ABI,omitempty"`
	OpenAPI      string `json:"openapi,omitempty"`
	RegisteredAs string `json:"registeredAs,omitempty"`
}

func PublishABI(ethconnectUrl string, contract *types.Contract) (*PublishAbiResponseBody, error) {
	u, err := url.Parse(ethconnectUrl)
	if err != nil {
		return nil, err
	}
	u, err = u.Parse("abis")
	if err != nil {
		return nil, err
	}
	requestUrl := u.String()
	abi, err := json.Marshal(contract.ABI)
	if err != nil {
		return nil, err
	}
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	fw, err := writer.CreateFormField("abi")
	if err != nil {
		return nil, err
	}
	if _, err := io.Copy(fw, bytes.NewReader(abi)); err != nil {
		return nil, err
	}
	fw, err = writer.CreateFormField("bytecode")
	if err != nil {
		return nil, err
	}
	if _, err = io.Copy(fw, strings.NewReader(contract.Bytecode)); err != nil {
		return nil, err
	}
	writer.Close()
	req, err := http.NewRequest("POST", requestUrl, bytes.NewReader(body.Bytes()))
	if err != nil {
		return nil, err
	}
	req.Header.Add("Content-Type", writer.FormDataContentType())
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	responseBody, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("%d %s", resp.StatusCode, responseBody)
	}
	var publishAbiResponse *PublishAbiResponseBody
	json.Unmarshal(responseBody, &publishAbiResponse)
	return publishAbiResponse, nil
}

func DeployContract(ethconnectUrl string, abiId string, fromAddress string, params map[string]string, registeredName string) (*DeployContractResponseBody, error) {
	u, err := url.Parse(ethconnectUrl)
	if err != nil {
		return nil, err
	}
	u, err = u.Parse(path.Join("abis", abiId))
	if err != nil {
		return nil, err
	}
	requestUrl := u.String()
	requestBody, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest("POST", requestUrl, bytes.NewBuffer(requestBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-firefly-from", fromAddress)
	req.Header.Set("x-firefly-sync", "true")
	if registeredName != "" {
		req.Header.Set("x-firefly-register", registeredName)
	}
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	responseBody, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("%d %s", resp.StatusCode, responseBody)
	}
	var deployContractResponse *DeployContractResponseBody
	json.Unmarshal(responseBody, &deployContractResponse)
	return deployContractResponse, nil
}

func RegisterContract(ethconnectUrl string, abiId string, contractAddress string, fromAddress string, registeredName string, params map[string]string) (*RegisterResponseBody, error) {
	u, err := url.Parse(ethconnectUrl)
	if err != nil {
		return nil, err
	}
	u, err = u.Parse(path.Join("abis", abiId, contractAddress))
	if err != nil {
		return nil, err
	}
	requestUrl := u.String()
	req, err := http.NewRequest("POST", requestUrl, bytes.NewBuffer(nil))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-firefly-sync", "true")
	req.Header.Set("x-firefly-register", registeredName)
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	responseBody, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != 201 {
		return nil, fmt.Errorf("%d %s", resp.StatusCode, responseBody)
	}
	var registerResponseBody *RegisterResponseBody
	json.Unmarshal(responseBody, &registerResponseBody)
	return registerResponseBody, nil
}
