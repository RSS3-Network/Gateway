package handlers

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/naturalselectionlabs/rss3-gateway/internal/database/dialer/cockroachdb/table"
	"github.com/naturalselectionlabs/rss3-gateway/internal/service/gateway/model"
	rules "github.com/naturalselectionlabs/rss3-gateway/internal/service/gateway/ru_rules"
	"github.com/rss3-network/gateway-common/accesslog"
)

func (app *App) ProcessAccessLog(accessLog *accesslog.Log) {
	rctx := context.Background()

	// Check billing eligibility
	if accessLog.KeyID == nil {
		return
	}

	// Find user
	keyIDParsed, err := strconv.ParseUint(*accessLog.KeyID, 10, 64)

	if err != nil {
		log.Printf("Failed to recover key id with error: %v", err)
		return
	}

	key, _, err := model.KeyGetByID(rctx, keyIDParsed, false, app.databaseClient, app.controlClient) // Deleted key could also be used for pending bills

	if err != nil {
		log.Printf("Failed to get key by id with error: %v", err)

		return
	}

	user, err := key.GetAccount(rctx)

	if err != nil {
		// Failed to get account
		log.Printf("Faield to get account with error: %v", err)

		return
	}

	if accessLog.Status != http.StatusOK || key.Account.IsPaused {
		err = key.ConsumeRu(rctx, 0) // Request failed or is in free tier, only increase API call count
		if err != nil {
			// Failed to consumer RU
			log.Printf("Faield to increase API call count with error: %v", err)
		}

		return
	}

	// Consumer RU
	pathSplits := strings.Split(accessLog.Path, "/")
	ruCalculator, ok := rules.Prefix2RUCalculator[pathSplits[1]]

	if !ok {
		// Invalid path
		log.Printf("No matching route prefix")

		return
	}

	ru := ruCalculator(fmt.Sprintf("/%s", strings.Join(pathSplits[2:], "/")))
	err = key.ConsumeRu(rctx, ru)

	if err != nil {
		// Failed to consume RU
		log.Printf("Faield to consume RU with error: %v", err)

		return
	}

	ruRemain, err := user.GetBalance(rctx)

	if err != nil {
		// Failed to get remain RU
		log.Printf("Faield to get account remain RU with error: %v", err)

		return
	}

	if ruRemain < 0 {
		log.Printf("Insufficient remain RU, pause account")
		// Pause user account
		if !key.Account.IsPaused {
			err = app.controlClient.PauseAccount(rctx, key.Account.Address.Hex())
			if err != nil {
				log.Printf("Failed to pause account with error: %v", err)
			} else {
				err = app.databaseClient.WithContext(rctx).
					Model(&table.GatewayAccount{}).
					Where("address = ?", key.Account.Address).
					Update("is_paused", true).
					Error
				if err != nil {
					log.Printf("Failed to save paused account into db with error: %v", err)
				}
			}
		}
	}
}