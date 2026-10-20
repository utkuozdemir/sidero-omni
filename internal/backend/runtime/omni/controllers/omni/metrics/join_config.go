// Copyright (c) 2026 Sidero Labs, Inc.
//
// Use of this software is governed by the Business Source License
// included in the LICENSE file.

package metrics

import (
	"slices"

	"github.com/siderolabs/image-factory/pkg/schematic"
	"github.com/siderolabs/talos/pkg/machinery/config/configloader"

	"github.com/siderolabs/omni/client/pkg/omni/resources/omni"
	"github.com/siderolabs/omni/internal/backend/kernelargs"
)

const (
	joinConfigEmbedded    = "schematic_embedded_config"
	joinConfigKernelArgs  = "schematic_kernel_args"
	joinConfigCommandLine = "command_line_only"
	joinConfigOther       = "other"
	joinConfigInvalid     = "invalid_schematic"
)

// joinConfigLocation returns where the SideroLink join config of the machine is: in the schematic it runs, only on its kernel command line, or elsewhere.
func joinConfigLocation(ms *omni.MachineStatus) string {
	spec := ms.TypedSpec().Value

	if !spec.SchematicReady() {
		return "unknown"
	}

	if spec.GetSchematic().GetInvalid() {
		return joinConfigInvalid
	}

	running, err := schematic.Unmarshal([]byte(spec.GetSchematic().GetRaw()))
	if err != nil {
		return "unknown"
	}

	if embedded := running.Customization.EmbeddedMachineConfiguration; embedded != "" {
		if cfg, err := configloader.NewFromBytes([]byte(embedded)); err == nil && cfg.SideroLink() != nil {
			return joinConfigEmbedded
		}
	}

	if slices.ContainsFunc(running.Customization.ExtraKernelArgs, kernelargs.IsJoinArg) {
		return joinConfigKernelArgs
	}

	if kernelargs.IsJoinArg(spec.KernelCmdline) {
		return joinConfigCommandLine
	}

	return joinConfigOther
}
