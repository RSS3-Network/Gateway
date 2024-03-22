package middlewares

import (
	"github.com/rss3-network/gateway-common/control"
	"net/http"
	"regexp"

	"github.com/labstack/echo/v4"
	"github.com/naturalselectionlabs/rss3-gateway/internal/service/gateway/constants"
	"github.com/naturalselectionlabs/rss3-gateway/internal/service/gateway/jwt"
	"github.com/naturalselectionlabs/rss3-gateway/internal/service/gateway/model"
	"github.com/naturalselectionlabs/rss3-gateway/internal/service/gateway/utils"
	"gorm.io/gorm"
)

func authenticateUser(ctx echo.Context, jwtUser *jwt.User, databaseClient *gorm.DB, controlClient *control.StateClientWriter) (*model.Account, error) {
	account, _, err := model.AccountGetByAddress(ctx.Request().Context(), jwtUser.Address, databaseClient, controlClient)

	if err != nil {
		return nil, err
	}

	return account, nil
}

func ParseUserWithToken(c echo.Context, jwtClient *jwt.JWT) *jwt.User {
	authToken, err := c.Request().Cookie(constants.AuthTokenCookieName)

	if err != nil || authToken == nil {
		return nil
	}

	user, _ := jwtClient.ParseUser(authToken.Value)

	return user
}

var (
	SkipMiddlewarePaths = regexp.MustCompile("^/(users/|health)")
)

func UserAuthenticationMiddleware(databaseClient *gorm.DB, controlClient *control.StateClientWriter, jwtClient *jwt.JWT) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			// this is a hack to workaround codegen and echo router group issue
			// see: https://github.com/labstack/echo/issues/1737
			// otherwise we need to manually group the routes
			// see: https://github.com/labstack/echo/issues/1737#issuecomment-906721802
			if SkipMiddlewarePaths.MatchString(c.Path()) {
				return next(c)
			}

			user := ParseUserWithToken(c, jwtClient)

			if user == nil {
				return utils.SendJSONError(c, http.StatusUnauthorized)
			}

			// Authenticate user
			account, err := authenticateUser(c, user, databaseClient, controlClient)

			if err != nil || account == nil {
				return utils.SendJSONError(c, http.StatusUnauthorized)
			}

			// Set user in context
			c.Set("user", account)

			// Continue with the pipeline
			return next(c)
		}
	}
}
