package handlers

import (
	"net/http"
	"strconv"

	"github.com/labstack/echo/v4"
	"github.com/rss3-network/payment-processor/internal/service/hub/gen/oapi"
	"github.com/rss3-network/payment-processor/internal/service/hub/model"
	"github.com/rss3-network/payment-processor/internal/service/hub/utils"
	"github.com/samber/lo"
	"go.uber.org/zap"
)

func (app *App) DeleteKey(ctx echo.Context, keyID string) error {
	k, exist, err := app.getKey(ctx, keyID)
	if err != nil {
		zap.L().Error("DeleteKey getKey", zap.Error(err))
		return utils.SendJSONError(ctx, http.StatusInternalServerError)
	} else if !exist {
		return utils.SendJSONError(ctx, http.StatusNotFound)
	}

	err = k.Delete(ctx.Request().Context())
	if err != nil {
		zap.L().Error("DeleteKey Delete", zap.Error(err))
		return utils.SendJSONError(ctx, http.StatusInternalServerError)
	}

	return ctx.NoContent(http.StatusOK)
}

func (app *App) GenerateKey(ctx echo.Context) error {
	user := ctx.Get("user").(*model.Account)

	var req oapi.KeyInfoBody
	if err := ctx.Bind(&req); err != nil || req.Name == nil {
		return ctx.NoContent(http.StatusBadRequest)
	}

	k, err := model.KeyCreate(ctx.Request().Context(), user.Address, *req.Name, app.databaseClient, app.controlClient)
	if err != nil {
		zap.L().Error("GenerateKey", zap.Error(err))
		return utils.SendJSONError(ctx, http.StatusInternalServerError)
	}

	return ctx.JSON(http.StatusOK, createKeyResponse(k))
}

func (app *App) GetKey(ctx echo.Context, keyID string) error {
	k, exist, err := app.getKey(ctx, keyID)
	if err != nil {
		zap.L().Error("GetKey", zap.Error(err))
		return utils.SendJSONError(ctx, http.StatusInternalServerError)
	} else if !exist {
		return utils.SendJSONError(ctx, http.StatusNotFound)
	}

	return ctx.JSON(http.StatusOK, createKeyResponse(k))
}

func (app *App) GetKeys(ctx echo.Context) error {
	user := ctx.Get("user").(*model.Account)

	keys, err := user.ListKeys(ctx.Request().Context())
	if err != nil {
		zap.L().Error("GetKeys", zap.Error(err))
		return utils.SendJSONError(ctx, http.StatusInternalServerError)
	}

	resp := oapi.Keys{}
	for _, k := range keys {
		resp = append(resp, createKeyResponse(k))
	}

	return ctx.JSON(http.StatusOK, resp)
}

func (app *App) UpdateKeyInfo(ctx echo.Context, keyID string) error {
	var req oapi.KeyInfoBody
	if err := ctx.Bind(&req); err != nil || req.Name == nil {
		return ctx.NoContent(http.StatusBadRequest)
	}

	k, exist, err := app.getKey(ctx, keyID)
	if err != nil {
		zap.L().Error("UpdateKeyInfo getKey", zap.Error(err))
		return utils.SendJSONError(ctx, http.StatusInternalServerError)
	} else if !exist {
		return utils.SendJSONError(ctx, http.StatusNotFound)
	}

	err = k.UpdateInfo(ctx.Request().Context(), *req.Name)
	if err != nil {
		zap.L().Error("UpdateKeyInfo UpdateInfo", zap.Error(err))
		return utils.SendJSONError(ctx, http.StatusInternalServerError)
	}

	return ctx.JSON(http.StatusOK, createKeyResponse(k))
}

func (app *App) RotateKey(ctx echo.Context, keyID string) error {
	k, exist, err := app.getKey(ctx, keyID)
	if err != nil {
		zap.L().Error("RotateKey getKey", zap.Error(err))
		return utils.SendJSONError(ctx, http.StatusInternalServerError)
	} else if !exist {
		return utils.SendJSONError(ctx, http.StatusNotFound)
	}

	err = k.Rotate(ctx.Request().Context())
	if err != nil {
		zap.L().Error("RotateKey Rotate", zap.Error(err))
		return utils.SendJSONError(ctx, http.StatusInternalServerError)
	}

	return ctx.JSON(http.StatusOK, createKeyResponse(k))
}

func createKeyResponse(k *model.Key) oapi.Key { // Assuming KeyType is the type of k
	return oapi.Key{
		Id:              lo.ToPtr(strconv.FormatUint(k.ID, 10)),
		Key:             lo.ToPtr(k.Key.String()),
		Name:            &k.Name,
		ApiCallsTotal:   &k.APICallsTotal,
		ApiCallsCurrent: &k.APICallsCurrent,
		RuUsedTotal:     &k.RuUsedTotal,
		RuUsedCurrent:   &k.RuUsedCurrent,
	}
}
