package handlers

import (
	"net/http"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/rss3-network/payment-processor/internal/service/hub/constants"
	"github.com/rss3-network/payment-processor/internal/service/hub/gen/oapi"
	"github.com/rss3-network/payment-processor/internal/service/hub/jwt"
	"github.com/rss3-network/payment-processor/internal/service/hub/model"
	"github.com/rss3-network/payment-processor/internal/service/hub/utils"
	"github.com/samber/lo"
	"go.uber.org/zap"
)

// SIWEGetNonce implements oapi.ServerInterface
func (app *App) SIWEGetNonce(ctx echo.Context) error {
	// Get nonce
	nonce, err := app.siweClient.GetNonce(ctx.Request().Context())

	if err != nil {
		zap.L().Error("SIWEGetNonce", zap.Error(err))
		return utils.SendJSONError(ctx, http.StatusInternalServerError)
	}

	// Return
	return ctx.String(http.StatusOK, nonce)
}

// SIWEGetSession implements oapi.ServerInterface
func (app *App) SIWEGetSession(ctx echo.Context) error {
	res := oapi.SIWESessionResponse{}

	_, user := app.getCtx(ctx)

	if user != nil {
		res.Address = lo.ToPtr(user.Address.Hex())
		res.ChainId = &user.ChainID
	}

	// User has logged in
	return ctx.JSON(http.StatusOK, res)
}

// SIWEVerify implements oapi.ServerInterface
func (app *App) SIWEVerify(ctx echo.Context) error {
	var req oapi.SIWEVerifyBody

	if err := ctx.Bind(&req); err != nil || req.Message == nil || req.Signature == nil {
		return ctx.NoContent(http.StatusBadRequest)
	}

	address, chainID, err := app.siweClient.ValidateSIWESignature(ctx.Request().Context(), *req.Message, *req.Signature)

	if err != nil {
		return utils.SendJSONError(ctx, http.StatusUnauthorized)
	}

	// get or create account
	acc, err := model.AccountGetOrCreate(ctx.Request().Context(), *address, app.databaseClient, app.controlClient)

	if err != nil {
		zap.L().Error("SIWEVerify AccountGetOrCreate", zap.Error(err))
		return utils.SendJSONError(ctx, http.StatusInternalServerError)
	}

	// set User with expiration
	expires := time.Now().Add(constants.AuthTokenDuration)
	token, err := app.jwtClient.SignToken(&jwt.User{
		Address: acc.Address,
		ChainID: chainID,
		Expires: expires.Unix(),
	})

	if err != nil {
		zap.L().Error("SIWEVerify JWT SignToken", zap.Error(err))
		return utils.SendJSONError(ctx, http.StatusInternalServerError)
	}

	cookie := &http.Cookie{
		Name:     constants.AuthTokenCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteNoneMode,
		Expires:  expires,
		Domain:   app.siweClient.Domain(),
	}

	ctx.SetCookie(cookie)

	return ctx.NoContent(http.StatusOK)
}

// SIWELogout implements oapi.ServerInterface
func (app *App) SIWELogout(ctx echo.Context) error {
	cookie := &http.Cookie{
		Name:     constants.AuthTokenCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteNoneMode,
		Expires:  time.Unix(0, 0),
		Domain:   app.siweClient.Domain(),
	}

	ctx.SetCookie(cookie)

	return ctx.NoContent(http.StatusOK)
}
