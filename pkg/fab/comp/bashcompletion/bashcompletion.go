// Copyright 2024 Hedgehog
// SPDX-License-Identifier: Apache-2.0

package bashcompletion

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"go.githedgehog.com/fabric/pkg/util/logutil"
	fabapi "go.githedgehog.com/fabricator/api/fabricator/v1beta1"
	"go.githedgehog.com/fabricator/api/meta"
)

const (
	BashCompletionRef = "bash-completion"
	TarballName       = "bash-completion.tar.xz"
	InstallDir        = "/opt/bash-completion"
	CompletionsDir    = InstallDir + "/completions"
	HookFilename      = "kubectl-fabric-hook.sh"
	ProfileDir        = "/etc/profile.d"
	ProfileFilename   = "bash-completion.sh"
)

func Version(f fabapi.Fabricator) meta.Version {
	return f.Status.Versions.Platform.BashCompletion
}

func Install(ctx context.Context, workDir string, fab fabapi.Fabricator) error {
	slog.Info("Installing bash-completion")

	versionStr := strings.TrimPrefix(string(Version(fab)), "v")

	tempExtractDir := fmt.Sprintf("/tmp/bash-completion-%s", versionStr)

	slog.Info("Using bash-completion version", "version", versionStr, "extractDir", tempExtractDir)

	if err := os.MkdirAll(InstallDir, 0o755); err != nil {
		return fmt.Errorf("creating bash-completion dir %q: %w", InstallDir, err)
	}

	tarballPath := filepath.Join(workDir, TarballName)

	if _, err := os.Stat(tarballPath); os.IsNotExist(err) {
		return fmt.Errorf("tarball not found at %s", tarballPath) //nolint:goerr113
	}

	listCmd := exec.CommandContext(ctx, "tar", "-tvf", tarballPath)
	listCmd.Dir = workDir
	listCmd.Stdout = logutil.NewSink(ctx, slog.Debug, "tarball contents: ")
	listCmd.Stderr = logutil.NewSink(ctx, slog.Debug, "tarball error: ")
	if err := listCmd.Run(); err != nil {
		return fmt.Errorf("listing tarball contents: %w", err)
	}

	slog.Info("Extracting bash-completion tarball", "path", tarballPath)
	cmd := exec.CommandContext(ctx, "tar", "-xvf", tarballPath, "-C", "/tmp")
	cmd.Dir = workDir
	cmd.Stdout = logutil.NewSink(ctx, slog.Debug, "bash-completion extract: ")
	cmd.Stderr = logutil.NewSink(ctx, slog.Debug, "bash-completion extract error: ")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("extracting bash-completion: %w", err)
	}

	if _, err := os.Stat(tempExtractDir); os.IsNotExist(err) {
		lsCmd := exec.CommandContext(ctx, "ls", "-la", "/tmp")
		lsCmd.Stdout = logutil.NewSink(ctx, slog.Info, "tmp contents: ")
		if err := lsCmd.Run(); err != nil {
			slog.Debug("Failed to list /tmp directory", "error", err)
		}

		return fmt.Errorf("extracted directory %s does not exist", tempExtractDir) //nolint:goerr113
	}

	lsCmd := exec.CommandContext(ctx, "ls", "-la", tempExtractDir)
	lsCmd.Stdout = logutil.NewSink(ctx, slog.Info, "extracted contents: ")
	if err := lsCmd.Run(); err != nil {
		slog.Debug("Failed to list extracted directory", "error", err)
	}

	if err := os.MkdirAll(CompletionsDir, 0o755); err != nil {
		return fmt.Errorf("creating completions dir: %w", err)
	}

	srcCompletions := filepath.Join(tempExtractDir, "completions")

	if _, err := os.Stat(srcCompletions); os.IsNotExist(err) {
		return fmt.Errorf("source completions directory %s does not exist", srcCompletions) //nolint:goerr113
	}

	slog.Info("Copying bash completions", "from", srcCompletions, "to", InstallDir)
	cmd = exec.CommandContext(ctx, "cp", "-rv", srcCompletions, InstallDir)
	cmd.Stdout = logutil.NewSink(ctx, slog.Debug, "cp: ")
	cmd.Stderr = logutil.NewSink(ctx, slog.Debug, "cp error: ")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("copying bash-completion files: %w", err) //nolint:goerr113
	}

	srcBashCompletion := filepath.Join(tempExtractDir, "bash_completion")

	if _, err := os.Stat(srcBashCompletion); os.IsNotExist(err) {
		return fmt.Errorf("source bash_completion file %s does not exist", srcBashCompletion) //nolint:goerr113
	}

	slog.Info("Copying bash_completion file", "from", srcBashCompletion, "to", InstallDir)
	cmd = exec.CommandContext(ctx, "cp", "-v", srcBashCompletion, InstallDir)
	cmd.Stdout = logutil.NewSink(ctx, slog.Debug, "cp: ")
	cmd.Stderr = logutil.NewSink(ctx, slog.Debug, "cp error: ")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("copying bash_completion file: %w", err)
	}

	if err := os.RemoveAll(tempExtractDir); err != nil {
		slog.Warn("Failed to clean up bash-completion tmp files", "error", err)
	}

	if err := InstallKubectlFabricCompletion(); err != nil {
		return fmt.Errorf("installing kubectl-fabric completion: %w", err)
	}

	if err := os.MkdirAll(ProfileDir, 0o755); err != nil {
		return fmt.Errorf("creating profile.d directory: %w", err)
	}

	profileScript := `#!/bin/bash
# Source bash completion
source /opt/bash-completion/bash_completion

# Set up kubectl alias and completion
alias k=kubectl
source <(kubectl completion bash)
complete -o default -F __start_kubectl k

# Source kubectl fabric hook
source /opt/bash-completion/kubectl-fabric-hook.sh
`

	profilePath := filepath.Join(ProfileDir, ProfileFilename)
	if err := os.WriteFile(profilePath, []byte(profileScript), 0o600); err != nil {
		return fmt.Errorf("writing bash-completion profile script: %w", err)
	}

	return nil
}

func InstallKubectlFabricCompletion() error {
	if err := os.MkdirAll(CompletionsDir, 0o755); err != nil {
		return fmt.Errorf("creating kubectl-fabric completions dir: %w", err)
	}

	fabricScript := `#!/bin/bash

# Dynamic kubectl-fabric completion script that parses help output
_kubectl_fabric() {
    local cur prev words cword
    _get_comp_words_by_ref -n : cur prev words cword

    # Capture the current command up to the completion point
    local cmd=()
    for ((i=0; i<cword; i++)); do
        cmd+=("${words[i]}")
    done
    
    # Find position of kubectl and fabric
    local kubectl_pos fabric_pos
    kubectl_pos=-1
    fabric_pos=-1
    
    for ((i=0; i<${#words[@]}; i++)); do
        if [[ ${words[i]} == "kubectl" || ${words[i]} == "k" ]]; then
            kubectl_pos=$i
        elif [[ ${words[i]} == "fabric" ]]; then
            fabric_pos=$i
        fi
    done
    
    # If we can't find kubectl or fabric, exit
    if [[ $kubectl_pos -eq -1 || $fabric_pos -eq -1 ]]; then
        return 0
    fi
    
    # Dynamically parse the help output to find available commands
    local command_string="${cmd[@]}"
    local help_output
    local available_commands=""
    
    # Special case for the top-level fabric command which we already know
    if [[ $cword -eq $((fabric_pos+1)) ]]; then
        available_commands="vpc switch sw connection conn switchgroup sg external ext wiring inspect i help h"
        COMPREPLY=($(compgen -W "$available_commands" -- "$cur"))
        return 0
    fi
    
    # Get help output for the current command path
    help_output=$(${cmd[@]} -h 2>&1)
    
    # Parse commands from the help output
    if [[ -n "$help_output" ]]; then
        # Look for a COMMANDS: section in the help output
        if [[ "$help_output" == *"COMMANDS:"* ]]; then
            # Extract commands section
            commands_section=$(echo "$help_output" | awk '/COMMANDS:/{flag=1;next}/OPTIONS:|GLOBAL OPTIONS:/{flag=0}flag')
            
            # Parse commands and aliases
            while read -r line; do
                if [[ "$line" =~ ^[[:space:]]*([a-zA-Z0-9_-]+)(,[[:space:]]*([a-zA-Z0-9_-]+))?[[:space:]]+ ]]; then
                    cmd_name="${BASH_REMATCH[1]}"
                    cmd_alias="${BASH_REMATCH[3]}"
                    
                    if [[ -n "$available_commands" ]]; then
                        available_commands="$available_commands $cmd_name"
                    else
                        available_commands="$cmd_name"
                    fi
                    
                    if [[ -n "$cmd_alias" ]]; then
                        available_commands="$available_commands $cmd_alias"
                    fi
                fi
            done <<< "$commands_section"
            
            # Also add standard help commands
            available_commands="$available_commands help h"
        fi
    fi
    
    # Complete with available commands
    if [[ -n "$available_commands" ]]; then
        COMPREPLY=($(compgen -W "$available_commands" -- "$cur"))
    fi
    
    return 0
}

complete -F _kubectl_fabric kubectl-fabric
`
	fabricPath := filepath.Join(CompletionsDir, "kubectl-fabric")
	// Using 0o600 permissions instead of 0o755
	if err := os.WriteFile(fabricPath, []byte(fabricScript), 0o600); err != nil {
		return fmt.Errorf("writing kubectl-fabric completion script: %w", err)
	}

	// Create hook for kubectl fabric completion
	hookScript := `#!/bin/bash

# Hook into kubectl completion to add fabric plugin completion
_kubectl_fabric_hook() {
    local cur prev words cword
    _init_completion -s || return

    # Check if we're dealing with fabric
    for ((i=0; i<${#words[@]}; i++)); do
        if [[ ${words[i]} == "fabric" ]]; then
            # Source and call the fabric completion function
            source /opt/bash-completion/completions/kubectl-fabric
            _kubectl_fabric
            return $?
        fi
    done

    # Continue with normal kubectl completion
    __start_kubectl "$@"
    return $?
}

# Override kubectl completion with our extended version
complete -o default -F _kubectl_fabric_hook kubectl
complete -o default -F _kubectl_fabric_hook k
`
	hookPath := filepath.Join(InstallDir, HookFilename)
	// Using 0o600 permissions instead of 0o755
	if err := os.WriteFile(hookPath, []byte(hookScript), 0o600); err != nil {
		return fmt.Errorf("writing kubectl-fabric hook script: %w", err)
	}

	return nil
}
