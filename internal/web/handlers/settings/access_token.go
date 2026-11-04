package settings

import (
	"errors"
	"strconv"
	"time"

	"github.com/devemmm/codes/internal/config"
	"github.com/devemmm/codes/internal/db"
	"github.com/devemmm/codes/internal/i18n"
	"github.com/devemmm/codes/internal/validator"
	"github.com/devemmm/codes/internal/web/context"
	"github.com/gorilla/sessions"
	"gorm.io/gorm"
)

const (
	pendingTokenValueKey  = "pending-access-token-value"
	pendingTokenExpiryKey = "pending-access-token-expires"
	pendingTokenIDKey     = "pending-access-token-id"
	pendingTokenLifetime  = 5 * time.Minute
)

var errTokenDisplayPending = errors.New("an access token is already waiting to be displayed")

func AccessTokens(ctx *context.Context) error {
	user := ctx.User

	ctx.SetData("apiEnabled", config.C.ApiEnabled)
	ctx.SetData("userIsAdmin", user.IsAdmin)
	ctx.SetData("settingsHeaderPage", "tokens")
	ctx.SetData("htmlTitle", ctx.TrH("settings"))
	var pendingToken string
	var pendingTokenID uint
	err := ctx.UpdateSession(func(sess *sessions.Session) error {
		value, _ := sess.Values[pendingTokenValueKey].(string)
		expires, _ := sess.Values[pendingTokenExpiryKey].(int64)
		tokenID, _ := sess.Values[pendingTokenIDKey].(uint)
		delete(sess.Values, pendingTokenValueKey)
		delete(sess.Values, pendingTokenExpiryKey)
		delete(sess.Values, pendingTokenIDKey)
		if value == "" || tokenID == 0 {
			return nil
		}
		token, err := db.GetAccessTokenByID(tokenID)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		if token.UserID != user.ID {
			return nil
		}
		pendingTokenID = tokenID
		if time.Now().Unix() < expires {
			pendingToken = value
			return nil
		}
		return token.Delete()
	})
	if err != nil {
		if pendingTokenID != 0 {
			if token, lookupErr := db.GetAccessTokenByID(pendingTokenID); lookupErr == nil {
				if deleteErr := token.Delete(); deleteErr != nil {
					err = errors.Join(err, deleteErr)
				}
			}
		}
		return ctx.ErrorRes(500, "Cannot consume access token display session", err)
	}
	tokens, err := db.GetAccessTokensByUserID(user.ID)
	if err != nil {
		return ctx.ErrorRes(500, "Cannot get access tokens", err)
	}
	ctx.SetData("accessTokens", tokens)
	if pendingToken != "" {
		ctx.SetData("newAccessToken", pendingToken)
		ctx.Response().Header().Set("Cache-Control", "no-store")
		ctx.Response().Header().Set("Pragma", "no-cache")
	}
	return ctx.Html("settings_tokens.html")
}

func AccessTokensProcess(ctx *context.Context) error {
	user := ctx.User

	dto := new(db.AccessTokenDTO)
	if err := ctx.Bind(dto); err != nil {
		return ctx.ErrorRes(400, ctx.Tr("error.cannot-bind-data"), err)
	}

	if err := ctx.Validate(dto); err != nil {
		ctx.AddFlash(validator.ValidationMessages(&err, ctx.GetData("locale").(*i18n.Locale)), "error")
		return ctx.RedirectTo("/-/settings/access-tokens")
	}

	token := dto.ToAccessToken()
	token.UserID = user.ID

	plainToken, err := token.GenerateToken()
	if err != nil {
		return ctx.ErrorRes(500, "Cannot generate token", err)
	}

	var tokenID uint
	err = ctx.UpdateSession(func(sess *sessions.Session) error {
		if value, _ := sess.Values[pendingTokenValueKey].(string); value != "" {
			if expires, _ := sess.Values[pendingTokenExpiryKey].(int64); time.Now().Unix() < expires {
				return errTokenDisplayPending
			}
		}
		if err := token.Create(); err != nil {
			return err
		}
		tokenID = token.ID
		sess.Values[pendingTokenValueKey] = plainToken
		sess.Values[pendingTokenExpiryKey] = time.Now().Add(pendingTokenLifetime).Unix()
		sess.Values[pendingTokenIDKey] = token.ID
		return nil
	})
	if err != nil {
		if tokenID != 0 {
			if cleanupErr := token.Delete(); cleanupErr != nil {
				return ctx.ErrorRes(500, "Cannot persist token display session or clean up token", errors.Join(err, cleanupErr))
			}
		}
		if errors.Is(err, errTokenDisplayPending) {
			return ctx.ErrorRes(409, "An access token is already waiting to be displayed", nil)
		}
		return ctx.ErrorRes(500, "Cannot persist access token display session", err)
	}

	ctx.AddFlash(ctx.Tr("settings.token-created"), "success")
	return ctx.RedirectTo("/-/settings/access-tokens")
}

func AccessTokensDelete(ctx *context.Context) error {
	user := ctx.User
	tokenID, err := strconv.Atoi(ctx.Param("id"))
	if err != nil {
		return ctx.RedirectTo("/-/settings/access-tokens")
	}

	token, err := db.GetAccessTokenByID(uint(tokenID))
	if err != nil || token.UserID != user.ID {
		return ctx.RedirectTo("/-/settings/access-tokens")
	}

	if err := token.Delete(); err != nil {
		return ctx.ErrorRes(500, "Cannot delete access token", err)
	}

	ctx.AddFlash(ctx.Tr("settings.token-deleted"), "success")
	return ctx.RedirectTo("/-/settings/access-tokens")
}
