package gist

import (
	"github.com/devemmm/codes/internal/db"
	"github.com/devemmm/codes/internal/web/context"
)

func DeleteGist(ctx *context.Context) error {
	gist := ctx.GetData("gist").(*db.Gist)

	if err := gist.Delete(); err != nil {
		return ctx.ErrorRes(500, "Error deleting this gist", err)
	}
	gist.RemoveFromIndex()

	ctx.AddFlash(ctx.Tr("flash.gist.deleted"), "success")
	return ctx.RedirectTo("/")
}
