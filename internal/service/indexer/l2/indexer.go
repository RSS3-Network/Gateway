package l2

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"time"

	"github.com/avast/retry-go/v4"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/naturalselectionlabs/rss3-gateway/common/apisix"
	"github.com/naturalselectionlabs/rss3-gateway/contract/l2"
	"github.com/naturalselectionlabs/rss3-gateway/internal/database"
	"github.com/naturalselectionlabs/rss3-gateway/internal/service"
	"github.com/naturalselectionlabs/rss3-gateway/schema"
	"github.com/samber/lo"
	"go.uber.org/zap"
)

var _ service.Server = (*server)(nil)

type server struct {
	databaseClient    database.Client
	ethereumClient    *ethclient.Client
	chainID           *big.Int
	contractBilling   *l2.Billing
	checkpoint        *schema.Checkpoint
	blockNumberLatest uint64
	apisixClient      *apisix.Client // For account resume only
	ruPerToken        int64
}

func (s *server) Run(ctx context.Context) (err error) {
	// Load checkpoint from database.
	if s.checkpoint, err = s.databaseClient.FindCheckpoint(ctx, s.chainID.Uint64()); err != nil {
		return fmt.Errorf("get checkpoint: %w", err)
	}

	// Rollback to the specified block number state.
	if err := s.databaseClient.RollbackBlock(ctx, s.checkpoint.ChainID, s.checkpoint.BlockNumber); err != nil {
		return fmt.Errorf("rollback block: %w", err)
	}

	onRetry := retry.OnRetry(func(n uint, err error) {
		zap.L().Error("run indexer", zap.Error(err), zap.Uint("attempts", n))
	})

	retryIf := retry.RetryIf(func(err error) bool {
		return !errors.Is(err, context.Canceled)
	})

	return retry.Do(func() error { return s.run(ctx) }, retry.DelayType(retry.FixedDelay), retry.Delay(time.Second), retry.Attempts(30), onRetry, retryIf)
}

func (s *server) run(ctx context.Context) (err error) {
	for {
		// Refresh the latest block number.
		if s.blockNumberLatest, err = s.ethereumClient.BlockNumber(ctx); err != nil {
			return fmt.Errorf("get latest block number: %w", err)
		}

		zap.L().Info(
			"refreshed the latest block number",
			zap.Uint64("block.number.local", s.checkpoint.BlockNumber),
			zap.Uint64("block.number.latest", s.blockNumberLatest),
		)

		// Waiting for a new block to be minted.
		if s.checkpoint.BlockNumber >= s.blockNumberLatest {
			blockConfirmationTime := time.Second // TODO Redefine it.

			zap.L().Info(
				"waiting for a new block to be minted",
				zap.Uint64("block.number.local", s.checkpoint.BlockNumber),
				zap.Uint64("block.number.latest", s.blockNumberLatest),
				zap.Duration("block.confirmationTime", blockConfirmationTime),
			)

			time.Sleep(blockConfirmationTime)

			continue
		}

		blockNumberCurrent := s.checkpoint.BlockNumber + 1

		// Get current block (header and transactions).
		block, err := s.ethereumClient.BlockByNumber(ctx, new(big.Int).SetUint64(blockNumberCurrent))
		if err != nil {
			return fmt.Errorf("get block %d: %w", blockNumberCurrent, err)
		}

		// Get all receipts of the current block.
		receipts, err := s.ethereumClient.BlockReceipts(ctx, rpc.BlockNumberOrHashWithNumber(rpc.BlockNumber(blockNumberCurrent)))
		if err != nil {
			return fmt.Errorf("get receipts for block %d: %w", block.NumberU64(), err)
		}

		if err := s.index(ctx, block, receipts); err != nil {
			return fmt.Errorf("index block %d: %w", blockNumberCurrent, err)
		}
	}
}

func (s *server) index(ctx context.Context, block *types.Block, receipts types.Receipts) error {
	// Begin a database transaction for the block.
	databaseTransaction, err := s.databaseClient.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin database transaction: %w", err)
	}

	defer lo.Try(databaseTransaction.Rollback)

	header := block.Header()

	for _, receipt := range receipts {
		// Discard all contract creation transactions.
		if block.Transaction(receipt.TxHash).To() == nil {
			continue
		}

		// Discard all failed transactions.
		if receipt.Status != types.ReceiptStatusSuccessful {
			continue
		}

		for index, log := range receipt.Logs {
			// Discard all removed logs.
			if log.Removed {
				continue
			}

			// Discard all anonymous logs.
			if len(log.Topics) == 0 {
				continue
			}

			switch log.Address {
			case l2.ContractMap[s.chainID.Uint64()].AddressBillingProxy:
				if err := s.indexBillingLog(ctx, header, block.Transaction(log.TxHash), receipt, log, index, databaseTransaction); err != nil {
					return fmt.Errorf("index billing log %s %d: %w", log.TxHash, log.Index, err)
				}
			}
		}
	}

	// Update and save checkpoint to memory and database.
	s.checkpoint.BlockHash = block.Hash()
	s.checkpoint.BlockNumber = block.NumberU64()

	if err := databaseTransaction.SaveCheckpoint(ctx, s.checkpoint); err != nil {
		return fmt.Errorf("save checkpoint: %w", err)
	}

	if databaseTransaction.Commit() != nil {
		return fmt.Errorf("commit database transaction: %w", err)
	}

	return nil
}

func NewServer(ctx context.Context, databaseClient database.Client, apisixClient *apisix.Client, ruPerToken int64, config Config) (service.Server, error) {
	var (
		instance = server{
			databaseClient: databaseClient,
			apisixClient:   apisixClient,
			ruPerToken:     ruPerToken,
		}
		err error
	)

	if instance.ethereumClient, err = ethclient.DialContext(ctx, config.Endpoint); err != nil {
		return nil, err
	}

	if instance.chainID, err = instance.ethereumClient.ChainID(ctx); err != nil {
		return nil, fmt.Errorf("get chain id: %w", err)
	}

	contractAddresses := l2.ContractMap[instance.chainID.Uint64()]
	if contractAddresses == nil {
		return nil, fmt.Errorf("chain id %d is not supported", instance.chainID)
	}

	if instance.contractBilling, err = l2.NewBilling(contractAddresses.AddressBillingProxy, instance.ethereumClient); err != nil {
		return nil, err
	}

	return &instance, nil
}
