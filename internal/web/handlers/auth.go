package handlers

import (
	"github.com/devemmm/codes/internal/web/context"
)

type ContextAuthInfo struct {
	Context *context.Context
}

func (auth ContextAuthInfo) RequireLogin() (bool, error) {
	return auth.Context.GetData("RequireLogin") == true, nil
}

func (auth ContextAuthInfo) AllowGistsWithoutLogin() (bool, error) {
	return auth.Context.GetData("AllowGistsWithoutLogin") == true, nil
}
