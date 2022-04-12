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

package cmd

import (
	"errors"
	"fmt"
	"time"

	"github.com/briandowns/spinner"
	"github.com/hyperledger/firefly-cli/internal/docker"
	"github.com/hyperledger/firefly-cli/internal/log"
	"github.com/hyperledger/firefly-cli/internal/stacks"
	"github.com/hyperledger/firefly-cli/pkg/types"
	"github.com/spf13/cobra"
)

var startOptions types.StartOptions

var startCmd = &cobra.Command{
	Use:   "start <stack_name>",
	Short: "Start a stack",
	Long: `Start a stack

This command will start a stack and run it in the background.
`,
	RunE: func(cmd *cobra.Command, args []string) error {
		var spin *spinner.Spinner
		if fancyFeatures && !verbose {
			spin = spinner.New(spinner.CharSets[11], 100*time.Millisecond)
			spin.FinalMSG = "done"
			logger = &log.SpinnerLogger{
				Spinner: spin,
			}
		}

		if err := docker.CheckDockerConfig(); err != nil {
			return err
		}

		stackManager := stacks.NewStackManager(logger)
		if len(args) == 0 {
			return errors.New("no stack specified")
		}
		stackName := args[0]

		if err := stackManager.LoadStack(stackName, verbose); err != nil {
			return err
		}

		if runBefore, err := stackManager.StackHasRunBefore(); err != nil {
			return err
		} else if !runBefore {
			fmt.Println("this will take a few seconds longer since this is the first time you're running this stack...")
		}

		if spin != nil {
			spin.Start()
		}
		if err := stackManager.StartStack(verbose, &startOptions); err != nil {
			return err
		}
		if spin != nil {
			spin.Stop()
		}
		fmt.Print("\n\n")
		for _, member := range stackManager.Stack.Members {
			fmt.Printf("Web UI for member '%v': http://127.0.0.1:%v/ui\n", member.ID, member.ExposedFireflyPort)
			if stackManager.Stack.SandboxEnabled {
				fmt.Printf("Sandbox UI for member '%v': http://127.0.0.1:%v\n\n", member.ID, member.ExposedSandboxPort)
			}
		}

		if stackManager.Stack.PrometheusEnabled {
			fmt.Printf("Web UI for shared Prometheus: http://127.0.0.1:%v\n", stackManager.Stack.ExposedPrometheusPort)
		}

		fmt.Printf("\nTo see logs for your stack run:\n\n%s logs %s\n\n", rootCmd.Use, stackName)
		return nil
	},
}

func init() {
	startCmd.Flags().BoolVarP(&startOptions.NoRollback, "no-rollback", "b", false, "Do not automatically rollback changes if first time setup fails")
	rootCmd.AddCommand(startCmd)
}
