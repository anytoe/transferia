package tasks

import (
	"context"

	"github.com/transferia/transferia/internal/logger"
	"github.com/transferia/transferia/library/go/core/xerrors"
	"github.com/transferia/transferia/pkg/abstract/coordinator"
	"github.com/transferia/transferia/pkg/abstract/model"
	"github.com/transferia/transferia/pkg/config/env"
	"github.com/transferia/transferia/pkg/errors"
	"github.com/transferia/transferia/pkg/errors/categories"
)

func StartJob(ctx context.Context, cp coordinator.Coordinator, transfer model.Transfer, task *model.TransferOperation) error {
	if !transfer.IsMain() {
		return nil
	}

	// check if current transfer operation is async - this flag is recalculated and passed by control plane
	if transfer.AsyncOperations {
		logger.Log.Infof("Transfer is async, will not start job")
		return nil
	}

	transfer.Status = model.Running
	if transfer.SnapshotOnly() {
		transfer.Status = model.Completed
	}
	if err := cp.SetStatus(transfer.ID, transfer.Status); err != nil {
		return errors.CategorizedErrorf(categories.Internal, "Cannot transit transfer into the %s state: %w", string(transfer.Status), err)
	}

	if transfer.SnapshotOnly() {
		return nil
	}

	if err := startRuntime(ctx, cp, transfer, task); err != nil {
		return xerrors.Errorf("unable to prepare runtime hook: %w", err)
	}
	if env.IsTest() {
		return nil
	}

	logger.Log.Info("Transfer status change is considered completed")
	return nil
}

var startRuntime = func(ctx context.Context, cp coordinator.Coordinator, transfer model.Transfer, task *model.TransferOperation) error {
	return nil
}
