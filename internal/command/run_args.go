// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

// choudoufu fork file. Upstream removed mitchellh/cli in 1.14, and with it
// every command's Run(args []string) method and the RunCommand bridge they
// called. The fork still runs commands from a raw argument list in two
// places: the joint run (joint_cmd.go, joint_saved.go), which drives init,
// plan, show and apply in-process, and the fork's own tests. This file keeps
// that entry point, built on upstream's replacement parser
// (CommandLine.ParseDirect, the one upstream's own RunCommander test helper
// uses), so upstream's command files stay as upstream wrote them.

// RunCommand parses args against cmd's command line the way the binary
// does, then runs it.
func RunCommand(cmd Command, meta Meta, args []string) int {
	return RunCli("", cmd, meta, cmd.CommandLine.ParseDirect(meta.CommandContext(), args))
}

func (c *ApplyCommand) Run(rawArgs []string) int {
	if c.Destroy {
		return RunCommand(DestroyCommander(), c.Meta, rawArgs)
	}
	return RunCommand(ApplyCommander(), c.Meta, rawArgs)
}

func (c *ConsoleCommand) Run(rawArgs []string) int {
	return RunCommand(ConsoleCommander(), c.Meta, rawArgs)
}

func (c *FmtCommand) Run(rawArgs []string) int {
	return RunCommand(FmtCommander(c.input), c.Meta, rawArgs)
}

func (c *GetCommand) Run(rawArgs []string) int {
	return RunCommand(GetCommander(), c.Meta, rawArgs)
}

func (c *GraphCommand) Run(rawArgs []string) int {
	return RunCommand(GraphCommander(), c.Meta, rawArgs)
}

func (c *ImportCommand) Run(rawArgs []string) int {
	return RunCommand(ImportCommander(), c.Meta, rawArgs)
}

func (c *InitCommand) Run(rawArgs []string) int {
	return RunCommand(InitCommander(), c.Meta, rawArgs)
}

func (c *LoginCommand) Run(rawArgs []string) int {
	return RunCommand(LoginCommander(), c.Meta, rawArgs)
}

func (c *LogoutCommand) Run(rawArgs []string) int {
	return RunCommand(LogoutCommander(), c.Meta, rawArgs)
}

func (c *OutputCommand) Run(rawArgs []string) int {
	return RunCommand(OutputCommander(), c.Meta, rawArgs)
}

func (c *PlanCommand) Run(rawArgs []string) int {
	return RunCommand(PlanCommander(), c.Meta, rawArgs)
}

func (c *ProvidersCommand) Run(rawArgs []string) int {
	return RunCommand(ProvidersCommander(), c.Meta, rawArgs)
}

func (c *ProvidersLockCommand) Run(rawArgs []string) int {
	return RunCommand(ProvidersLockCommander(), c.Meta, rawArgs)
}

func (c *ProvidersMirrorCommand) Run(rawArgs []string) int {
	return RunCommand(ProvidersMirrorCommander(), c.Meta, rawArgs)
}

func (c *ProvidersSchemaCommand) Run(rawArgs []string) int {
	return RunCommand(ProvidersSchemaCommander(), c.Meta, rawArgs)
}

func (c *RefreshCommand) Run(rawArgs []string) int {
	return RunCommand(RefreshCommander(), c.Meta, rawArgs)
}

func (c *ShowCommand) Run(rawArgs []string) int {
	return RunCommand(ShowCommander(), c.Meta, rawArgs)
}

func (c *StateListCommand) Run(rawArgs []string) int {
	return RunCommand(StateListCommander(), c.Meta, rawArgs)
}

func (c *StateMvCommand) Run(rawArgs []string) int {
	return RunCommand(StateMvCommander(), c.Meta, rawArgs)
}

func (c *StatePullCommand) Run(rawArgs []string) int {
	return RunCommand(StatePullCommander(), c.Meta, rawArgs)
}

func (c *StatePushCommand) Run(rawArgs []string) int {
	return RunCommand(StatePushCommander(), c.Meta, rawArgs)
}

func (c *StateReplaceProviderCommand) Run(rawArgs []string) int {
	return RunCommand(StateReplaceProviderCommander(), c.Meta, rawArgs)
}

func (c *StateRmCommand) Run(rawArgs []string) int {
	return RunCommand(StateRmCommander(), c.Meta, rawArgs)
}

func (c *StateShowCommand) Run(rawArgs []string) int {
	return RunCommand(StateShowCommander(), c.Meta, rawArgs)
}

func (c *TaintCommand) Run(rawArgs []string) int {
	return RunCommand(TaintCommander(), c.Meta, rawArgs)
}

func (c *TestCommand) Run(rawArgs []string) int {
	return RunCommand(TestCommander(), c.Meta, rawArgs)
}

func (c *UnlockCommand) Run(rawArgs []string) int {
	return RunCommand(UnlockCommander(), c.Meta, rawArgs)
}

func (c *UntaintCommand) Run(rawArgs []string) int {
	return RunCommand(UntaintCommander(), c.Meta, rawArgs)
}

func (c *ValidateCommand) Run(rawArgs []string) int {
	return RunCommand(ValidateCommander(), c.Meta, rawArgs)
}

func (c *VersionCommand) Run(rawArgs []string) int {
	return RunCommand(VersionCommander(c.Version, c.VersionPrerelease, c.Platform), c.Meta, rawArgs)
}

func (c *WorkspaceDeleteCommand) Run(rawArgs []string) int {
	return RunCommand(WorkspaceDeleteCommander(c.LegacyName), c.Meta, rawArgs)
}

func (c *WorkspaceListCommand) Run(rawArgs []string) int {
	return RunCommand(WorkspaceListCommander(c.LegacyName), c.Meta, rawArgs)
}

func (c *WorkspaceNewCommand) Run(rawArgs []string) int {
	return RunCommand(WorkspaceNewCommander(c.LegacyName), c.Meta, rawArgs)
}

func (c *WorkspaceSelectCommand) Run(rawArgs []string) int {
	return RunCommand(WorkspaceSelectCommander(c.LegacyName), c.Meta, rawArgs)
}

func (c *WorkspaceShowCommand) Run(rawArgs []string) int {
	return RunCommand(WorkspaceShowCommander(), c.Meta, rawArgs)
}
