package agent

import (
	"fmt"
	"strings"

	"github.com/zariel/katl/internal/installer/operation"
	agentapi "github.com/zariel/katl/internal/katlc/agentapi"
)

const OperationKindDestructiveReset = "destructive-reset"

func destructiveResetFromProto(req *agentapi.DestructiveResetOperationRequest) operation.DestructiveReset {
	if req == nil {
		return operation.DestructiveReset{}
	}
	surfaces := make([]string, 0, len(req.WipeSurfaces))
	for _, surface := range req.WipeSurfaces {
		if trimmed := strings.TrimSpace(surface); trimmed != "" {
			surfaces = append(surfaces, trimmed)
		}
	}
	return operation.DestructiveReset{
		InventoryNodeName:      strings.TrimSpace(req.InventoryNodeName),
		ResetScope:             strings.TrimSpace(req.ResetScope),
		TargetGenerationID:     strings.TrimSpace(req.TargetGenerationId),
		DiscardClusterIdentity: req.DiscardClusterIdentity,
		WipeSurfaces:           surfaces,
	}
}

func validateDestructiveResetRequest(operationKind string, req *agentapi.DestructiveResetOperationRequest) error {
	if operationKind != OperationKindDestructiveReset {
		return fmt.Errorf("operationKind %q does not accept destructiveReset", operationKind)
	}
	reset := destructiveResetFromProto(req)
	if err := operation.ValidateDestructiveReset(reset); err != nil {
		return err
	}
	return nil
}
