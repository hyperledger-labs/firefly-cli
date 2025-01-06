# FireFly CLI

![build](https://github.com/hyperledger/firefly-cli/actions/workflows/build.yml/badge.svg?branch=main)

The FireFly CLI can be used to create local [FireFly](https://github.com/hyperledger/firefly) stacks
for offline development of blockchain apps. This allows developers to rapidly iterate on their idea without
needing to set up a bunch of infrastructure before they can write the first line of code.

![FireFly CLI Screenshot](docs/firefly_screenshot.png)

## Prerequisites

In order to run the FireFly CLI, you will need a few things installed on your dev machine:

- [Docker](https://www.docker.com/)
- [Docker Compose](https://docs.docker.com/compose/)
- openssl

## Install the CLI

The easiest way to get up and running with the FireFly CLI is to download a pre-compiled binary of the latest release.

### Download the package for your OS
Go to the [latest release page](https://github.com/hyperledger/firefly-cli/releases/latest) and download the package for your OS and CPU architecture.

### Extract the binary and move it to `/usr/local/bin`

Assuming you downloaded the package from GitHub into you `Downloads` directory, run the following command:

```
sudo tar -zxf ~/Downloads/firefly-cli_*.tar.gz -C /usr/local/bin ff
```

If you downloaded the package from GitHub into a different directory, you will need to change the `tar` command above to wherever the `firefly-cli_*.tar.gz ` file is located.

### macOSUsers
 > **NOTE**: On recent versions of macOS, default security settings will prevent the FireFly CLI binary from running, because it was downloaded from the internet. You will need to [allow the FireFly CLI in System Preferences](docs/mac_help.md), before it will run.

### Windows Users
 > **NOTE**: For Windows users, we recommend that you use [Windows Subsystem for Linux 2 (WSL2)](https://docs.microsoft.com/en-us/windows/wsl/). Binaries provided for Linux will work in this environment.

### Linux Users
> **NOTE**: For Linux users, it is recommended that you add your user to the `docker` group so that you do not have to run `ff` or `docker` as `root` or with `sudo`. For more information about Docker permissions on Linux, please see [Docker's documentation on the topic](https://docs.docker.com/engine/install/linux-postinstall/).

### Install via Go

If you have a local Go development environment, and you have included `${GOPATH}/bin` in your path, you can install with:

```sh
go install github.com/hyperledger/firefly-cli/ff@latest
```

## Create a new stack

```
$ ff init <stack_name>
```

### Stack Initialization Options

When initializing a new stack, you can configure various options including:

```
--from-block      For multiparty networks only: specify the starting block for event processing. 
                  Use 'newest' for latest block, or a specific block number (default: "0")
```

Examples:
```
# Initialize with default settings (starts from block 0)
ff init mystack

# Initialize starting from the latest block
ff init mystack --from-block newest

# Initialize starting from a specific block number
ff init mystack --from-block 1234567
```

## Start a stack

```
$ ff start <stack_name>
```

## View logs

```
$ ff logs <stack_name>
```

> **NOTE**: You can use the `-f` flag on the `logs` command to follow the log output from all nodes in the stack

## Stop a stack

```
$ ff stop <stack_name>
```

## Clear all data from a stack

This command clears all data in a stack, but leaves the stack itself. This is useful for testing when you want to start with a clean slate but don't want to actually recreate the resources in the stack itself. Note: this will also stop the stack if it is running.

```
$ ff reset <stack_name>
```

## Completely delete a stack

This command will completely delete a stack, including all of its data and configuration.

```
$ ff remove <stack_name>
```

## Get stack info

This command will print out information about a particular stack, including whether it is running or not.

```
$ ff info <stack_name>
```

## List all stacks

This command will list all stacks that have been created on your machine.

```
$ ff ls
```
