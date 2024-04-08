package l2

import (
	"context"
	"fmt"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/rss3-network/payment-processor/common/ethereum"
	"github.com/rss3-network/payment-processor/contract/l2"
	"github.com/rss3-network/payment-processor/internal/service/indexer/constants"
	"github.com/samber/lo"
	"go.uber.org/zap"
	"math/big"
	"time"
)

func (s *server) billingFlow(ctx context.Context) (*big.Int, error) {
	// billing
	var usersRequireRuLimitRefresh []common.Address

	// Step 1: Collect
	succeededUsers, totalCollected, err := s.billingCollect(ctx)

	if err != nil {
		return nil, err
	}

	if succeededUsers != nil {
		usersRequireRuLimitRefresh = append(usersRequireRuLimitRefresh, succeededUsers...)
	}

	// Step 2: Withdraw
	succeededUsers, err = s.billingWithdraw(ctx)

	if err != nil {
		return nil, err
	}

	if succeededUsers != nil {
		usersRequireRuLimitRefresh = append(usersRequireRuLimitRefresh, succeededUsers...)
	}

	// Step 3: Merge succeed lists and refresh their RU limit
	err = s.billingUpdateRuLimit(ctx, usersRequireRuLimitRefresh)

	if err != nil {
		return nil, err
	}

	return totalCollected, nil
}

func (s *server) billingCollect(ctx context.Context) ([]common.Address, *big.Int, error) {
	// billing collect tokens
	nowTime := time.Now() // Epoch round identifier for billing

	users, amounts, err := s.buildBillingCollectTokens(ctx, nowTime)

	if err != nil {
		zap.L().Error("build billing collect tokens", zap.Error(err))
		return nil, nil, fmt.Errorf("build billing collect tokens: %w", err)
	}

	if users == nil || amounts == nil {
		// Nothing to do
		return nil, big.NewInt(0), nil
	}

	// else Need collect
	var succeededUsers []common.Address
	totalCollected := big.NewInt(0)

	// call contract slice by slice
	for len(users) > 0 {
		limit := len(users)
		if limit > constants.BatchSize {
			limit = constants.BatchSize
		}

		err = s.triggerBillingCollectTokens(ctx, users[:limit], amounts[:limit])
		if err == nil {
			succeededUsers = append(succeededUsers, users[:limit]...)
			for _, amount := range amounts[limit:] {
				totalCollected.Add(totalCollected, amount)
			}
		}

		users = users[limit:]
		amounts = amounts[limit:]
	}

	zap.L().Info("Collect tokens ran successfully")

	return succeededUsers, totalCollected, nil
}

func (s *server) billingWithdraw(ctx context.Context) ([]common.Address, error) {
	// billing withdraw
	users, amounts, err := s.buildBillingWithdrawTokens(ctx)

	if err != nil {
		zap.L().Error("build billing withdraw", zap.Error(err))
		return nil, fmt.Errorf("build billing withdraw: %w", err)
	}

	if users == nil || amounts == nil {
		// Nothing to do
		return nil, nil
	}

	// else Need withdraw
	var succeededUsers []common.Address

	// Calculate fee
	currentGas, err := s.ethereumClient.SuggestGasPrice(ctx)
	if err != nil {
		zap.L().Error("get gas price", zap.Error(err))
		// fallback
		currentGas = big.NewInt(1) // TODO
	}
	fee, _ := new(big.Float).Mul(
		big.NewFloat(30_000), // TODO
		new(big.Float).SetInt(currentGas),
	).Int(nil)

	// call contract slice by slice
	for len(users) > 0 {
		limit := len(users)
		if limit > constants.BatchSize {
			limit = constants.BatchSize
		}

		err = s.triggerBillingWithdrawTokens(ctx, users[:limit], amounts[:limit], fee)
		if err == nil {
			succeededUsers = append(succeededUsers, users[:limit]...)
		}

		users = users[limit:]
		amounts = amounts[limit:]
	}

	return succeededUsers, nil
}

func (s *server) billingUpdateRuLimit(ctx context.Context, usersRequireRuLimitRefresh []common.Address) error {
	// update ru limit
	currentBalance, err := s.getCurrentRuBalance(ctx, usersRequireRuLimitRefresh)
	if err != nil {
		zap.L().Error("refresh ru limit", zap.Error(err), zap.Any("usersRequireRuLimitRefresh", usersRequireRuLimitRefresh))
	} else if currentBalance != nil {
		err = s.databaseClient.UpdateBillingRuLimit(ctx, currentBalance)
		if err != nil {
			zap.L().Error("update ru limit", zap.Error(err), zap.Any("usersRequireRuLimitRefresh", usersRequireRuLimitRefresh))
		}
	}

	return nil
}

func (s *server) buildBillingCollectTokens(ctx context.Context, nowTime time.Time) ([]common.Address, []*big.Int, error) {
	collectTokensData, err := s.databaseClient.PrepareBillingCollectTokens(ctx, nowTime)

	if err != nil {
		zap.L().Error("prepare billing data", zap.Error(err))
		return nil, nil, fmt.Errorf("prepare billing data: %w", err)
	}

	if collectTokensData == nil {
		// Nothing to do
		return nil, nil, nil
	}

	// Prepare result storage arrays
	var (
		users   []common.Address
		amounts []*big.Int
	)

	// Calculate consumed token (w/ billing rate) per address
	for addr, ruC := range *collectTokensData {
		consumedTokenRaw := new(big.Int).Quo(
			new(big.Int).Mul(big.NewInt(ruC.Ru), big.NewInt(ethereum.BillingTokenDecimals)),
			big.NewInt(s.ruPerToken),
		)

		consumedToken, _ := new(big.Float).Mul(
			new(big.Float).SetInt(consumedTokenRaw),
			big.NewFloat(ruC.BillingRate),
		).Int(nil)

		// Check address balance, prevent from exceeding
		balance, err := s.contractBilling.BalanceOf(&bind.CallOpts{
			Context: ctx,
		}, addr)

		if err == nil && consumedToken.Cmp(balance) == 1 {
			// Balance not enough, only get balance to prevent calculation exceeds
			consumedToken = balance
		}

		if consumedToken.Cmp(big.NewInt(0)) == 1 {
			// consumedTokenDecimal > 0
			users = append(users, addr)
			amounts = append(amounts, consumedToken)
		}
	}

	return users, amounts, nil
}

func (s *server) buildBillingWithdrawTokens(ctx context.Context) ([]common.Address, []*big.Int, error) {
	withdrawData, err := s.databaseClient.PrepareBillingWithdrawTokens(ctx)

	if err != nil {
		zap.L().Error("prepare billing data", zap.Error(err))
		return nil, nil, fmt.Errorf("prepare billing data: %w", err)
	}

	if withdrawData == nil {
		// Nothing to do
		return nil, nil, nil
	}

	// Prepare result storage arrays
	var (
		users   []common.Address
		amounts []*big.Int
	)

	for addr, withdrawAmount := range *withdrawData {
		amount, _ := new(big.Float).Mul(big.NewFloat(withdrawAmount), big.NewFloat(ethereum.BillingTokenDecimals)).Int(nil)

		if amount == nil {
			zap.L().Error("parse withdraw amount", zap.String("address", addr.Hex()), zap.Float64("amount", withdrawAmount))
		} else {
			users = append(users, addr)
			amounts = append(amounts, amount)
		}
	}

	return users, amounts, nil
}

func (s *server) triggerBillingCollectTokens(ctx context.Context, users []common.Address, amounts []*big.Int) error {
	// Trigger collectTokens contract.
	nonce, err := s.ethereumClient.PendingNonceAt(ctx, s.fromAddress)
	if err != nil {
		return fmt.Errorf("get pending nonce: %w", err)
	}

	gasPrice, err := s.ethereumClient.SuggestGasPrice(ctx)
	if err != nil {
		return fmt.Errorf("get gas price: %w", err)
	}

	input, err := s.encodeInput(l2.BillingMetaData.ABI, l2.MethodCollectTokens, users, amounts, s.toAddress)
	if err != nil {
		return fmt.Errorf("encode input: %w", err)
	}

	unsignedTX := types.NewTx(&types.LegacyTx{
		Nonce:    nonce,
		GasPrice: gasPrice,
		Gas:      s.gasLimit,
		To:       lo.ToPtr(l2.ContractMap[s.chainID.Uint64()].AddressBillingProxy),
		Value:    big.NewInt(0),
		Data:     input,
	})

	args := s.newTransactionArgsFromTransaction(s.chainID, s.fromAddress, unsignedTX)

	var result hexutil.Bytes
	if err = s.rpcClient.CallContext(ctx, &result, "eth_signTransaction", args); err != nil {
		return fmt.Errorf("eth_signTransaction failed: %w", err)
	}

	signedTX := &types.Transaction{}
	if err = signedTX.UnmarshalBinary(result); err != nil {
		return err
	}

	err = s.ethereumClient.SendTransaction(ctx, signedTX)
	if err != nil {
		zap.L().Error("collect tokens", zap.Error(err), zap.Any("users", users), zap.Any("amounts", amounts))
		s.ReportFailedTransactionToSlack(err, signedTX.Hash().Hex(), "Collect", users, amounts)

		return fmt.Errorf("prepare billing collect tokens contract call: %w", err)
	}

	// Wait for transaction receipt.
	if err = s.transactionReceipt(ctx, signedTX.Hash()); err != nil {
		zap.L().Error("wait for transaction receipt", zap.Error(err), zap.String("tx", signedTX.Hash().String()))
		s.ReportFailedTransactionToSlack(err, signedTX.Hash().Hex(), "Collect", users, amounts)

		return fmt.Errorf("wait for transaction receipt: %w", err)
	}

	zap.L().Info("collect tokens successfully", zap.String("tx", signedTX.Hash().String()))

	return nil
}

func (s *server) triggerBillingWithdrawTokens(ctx context.Context, users []common.Address, amounts []*big.Int, fee *big.Int) error {
	// Trigger collectTokens contract.
	// Prepare fees
	length := len(users)
	fees := make([]*big.Int, length)

	for i := 0; i < length; i++ {
		fees[i] = fee
	}

	nonce, err := s.ethereumClient.PendingNonceAt(ctx, s.fromAddress)
	if err != nil {
		return fmt.Errorf("get pending nonce: %w", err)
	}

	gasPrice, err := s.ethereumClient.SuggestGasPrice(ctx)
	if err != nil {
		return fmt.Errorf("get gas price: %w", err)
	}

	input, err := s.encodeInput(l2.BillingMetaData.ABI, l2.MethodWithdrawTokens, users, amounts, fees)
	if err != nil {
		return fmt.Errorf("encode input: %w", err)
	}

	unsignedTX := types.NewTx(&types.LegacyTx{
		Nonce:    nonce,
		GasPrice: gasPrice,
		Gas:      s.gasLimit,
		To:       lo.ToPtr(l2.ContractMap[s.chainID.Uint64()].AddressBillingProxy),
		Value:    big.NewInt(0),
		Data:     input,
	})

	args := s.newTransactionArgsFromTransaction(s.chainID, s.fromAddress, unsignedTX)

	var result hexutil.Bytes
	if err = s.rpcClient.CallContext(ctx, &result, "eth_signTransaction", args); err != nil {
		return fmt.Errorf("eth_signTransaction failed: %w", err)
	}

	signedTX := &types.Transaction{}
	if err = signedTX.UnmarshalBinary(result); err != nil {
		return err
	}

	err = s.ethereumClient.SendTransaction(ctx, signedTX)
	if err != nil {
		zap.L().Error("collect tokens", zap.Error(err), zap.Any("users", users), zap.Any("amounts", amounts))
		s.ReportFailedTransactionToSlack(err, signedTX.Hash().Hex(), "Withdraw", users, amounts)

		return fmt.Errorf("prepare billing collect tokens contract call: %w", err)
	}

	// Wait for transaction receipt.
	if err = s.transactionReceipt(ctx, signedTX.Hash()); err != nil {
		zap.L().Error("wait for transaction receipt", zap.Error(err), zap.String("tx", signedTX.Hash().String()))
		s.ReportFailedTransactionToSlack(err, signedTX.Hash().Hex(), "Withdraw", users, amounts)

		return fmt.Errorf("wait for transaction receipt: %w", err)
	}

	zap.L().Info("collect tokens successfully", zap.String("tx", signedTX.Hash().String()))

	return nil
}

func (s *server) getCurrentRuBalance(ctx context.Context, users []common.Address) (map[common.Address]int64, error) {
	if len(users) == 0 {
		return nil, nil
	}

	latestRuLimit := make(map[common.Address]int64)

	for _, address := range users {
		// Get from chain
		balance, err := s.contractBilling.BalanceOf(&bind.CallOpts{
			Context: ctx,
		}, address)

		if err != nil {
			// Something is wrong
			zap.L().Error("get current balance", zap.Error(err), zap.String("address", address.Hex()))
			continue
		}

		// Parse balance to RU
		parsedRu, _ := new(big.Float).Mul(new(big.Float).Quo(
			new(big.Float).SetInt(balance),
			new(big.Float).SetInt(big.NewInt(ethereum.BillingTokenDecimals)),
		), big.NewFloat(float64(s.ruPerToken))).Int64()

		latestRuLimit[address] = parsedRu
	}

	return latestRuLimit, nil
}
