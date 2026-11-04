package context

import (
	"context"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"sync"

	"github.com/devemmm/codes/internal/config"
	"github.com/devemmm/codes/internal/db"
	"github.com/devemmm/codes/internal/i18n"
	"github.com/gorilla/sessions"
	"github.com/labstack/echo/v4"
	"github.com/rs/zerolog/log"
)

type dataKey string

const DataKeyStr dataKey = "data"

type HTTPError struct {
	Internal error       `json:"-"`
	Message  interface{} `json:"message"`
	Code     int         `json:"status"`
}

func (he *HTTPError) Error() string {
	if he.Internal == nil {
		return fmt.Sprintf("code=%d, message=%v", he.Code, he.Message)
	}
	return fmt.Sprintf("code=%d, message=%v, internal=%v", he.Code, he.Message, he.Internal)
}

type Context struct {
	echo.Context

	data echo.Map
	lock sync.RWMutex

	store *Store
	User  *db.User
}

func NewContext(c echo.Context, sessionPath string) (*Context, error) {
	store, err := NewStore(sessionPath)
	if err != nil {
		return nil, err
	}
	return NewContextWithStore(c, store), nil
}

func NewContextWithStore(c echo.Context, store *Store) *Context {
	ctx := &Context{
		Context: c,
		data:    make(echo.Map),
		store:   store,
	}

	ctx.SetRequest(ctx.Request().WithContext(context.WithValue(ctx.Request().Context(), DataKeyStr, ctx.data)))
	return ctx
}

func (ctx *Context) SetData(key string, value any) {
	ctx.lock.Lock()
	defer ctx.lock.Unlock()

	ctx.data[key] = value
}

func (ctx *Context) GetData(key string) any {
	ctx.lock.RLock()
	defer ctx.lock.RUnlock()

	return ctx.data[key]
}

func (ctx *Context) DataMap() echo.Map {
	return ctx.data
}

func (ctx *Context) ErrorRes(code int, message string, err error) error {
	return ctx.errorRes(code, message, err)
}

func (ctx *Context) errorRes(code int, message string, err error) error {
	if code >= 500 && err != nil {
		var skipLogger = log.With().CallerWithSkipFrameCount(4).Logger()
		skipLogger.Error().Err(err).Msg(message)
	}

	ctx.SetRequest(ctx.Request().WithContext(context.WithValue(ctx.Request().Context(), DataKeyStr, ctx.data)))

	return &HTTPError{Code: code, Message: message, Internal: err}
}

func (ctx *Context) RedirectTo(location string) error {
	return ctx.Redirect(302, config.C.ExternalUrl+location)
}

func (ctx *Context) Html(template string) error {
	return ctx.HtmlWithCode(200, template)
}

func (ctx *Context) HtmlWithCode(code int, template string) error {
	ctx.setErrorFlashes()
	return ctx.Render(code, template, ctx.DataMap())
}

func (ctx *Context) Json(data any) error {
	return ctx.JsonWithCode(200, data)
}

func (ctx *Context) JsonWithCode(code int, data any) error {
	return ctx.JSON(code, data)
}

func (ctx *Context) ErrorJson(code int, message string, err error) error {
	ctx.SetData("err_render", "json")
	return ctx.errorRes(code, message, err)
}

func (ctx *Context) PlainText(code int, message string) error {
	return ctx.String(code, message)
}

func (ctx *Context) NotFound(message string) error {
	return ctx.ErrorRes(404, message, nil)
}

func (ctx *Context) GetSession() *sessions.Session {
	sess, _ := ctx.store.UserStore.Get(ctx.Request(), "session")
	return sess
}

func (ctx *Context) SaveSession(sess *sessions.Session) error {
	return sess.Save(ctx.Request(), ctx.Response())
}

func (ctx *Context) SaveSessionError(message string, err error) error {
	status := 500
	if errors.Is(err, ErrSessionConflict) || errors.Is(err, ErrSessionExpired) {
		status = 409
	}
	return ctx.ErrorRes(status, message, err)
}

// UpdateSession reloads and modifies the authenticated filesystem session
// under an OS-level lock shared by all Codes processes using this session path.
func (ctx *Context) UpdateSession(update func(*sessions.Session) error) error {
	store := ctx.store.fileStore
	if store == nil {
		store = ctx.store.UserStore
	}
	initialRequest := ctx.Request().Clone(context.Background())
	initial, err := store.Get(initialRequest, "session")
	if err != nil {
		return err
	}
	return ctx.store.withSessionLock(initial.ID, func() error {
		// Do not carry Gorilla's per-request registry into this reload: middleware
		// may already have cached a stale copy before the cross-process lock.
		req := ctx.Request().Clone(context.Background())
		sess, err := store.Get(req, "session")
		if err != nil {
			return err
		}
		if age, ok := sess.Values[sessionMaxAgeKey].(int); ok && age > 0 {
			sess.Options.MaxAge = age
		}
		if err := update(sess); err != nil {
			return err
		}
		return store.Save(req, ctx.Response(), sess)
	})
}

func (ctx *Context) DeleteSession() error {
	sess := ctx.GetSession()
	sess.Options.MaxAge = -1
	return ctx.SaveSession(sess)
}

func (ctx *Context) AddFlash(flashMessage string, flashType string) {
	sess, _ := ctx.store.flashStore.Get(ctx.Request(), "flash")
	sess.AddFlash(flashMessage, flashType)
	_ = sess.Save(ctx.Request(), ctx.Response())
}

func (ctx *Context) setErrorFlashes() {
	sess, _ := ctx.store.flashStore.Get(ctx.Request(), "flash")

	ctx.SetData("flashErrors", sess.Flashes("error"))
	ctx.SetData("flashSuccess", sess.Flashes("success"))
	ctx.SetData("flashWarnings", sess.Flashes("warning"))

	_ = sess.Save(ctx.Request(), ctx.Response())
}

func (ctx *Context) DeleteCsrfCookie() {
	ctx.SetCookie(&http.Cookie{Name: "_csrf", Path: "/", MaxAge: -1})
}

func (ctx *Context) TrH(key string, args ...any) template.HTML {
	l := ctx.GetData("locale").(*i18n.Locale)
	return l.Tr(key, args...)
}

func (ctx *Context) Tr(key string, args ...any) string {
	l := ctx.GetData("locale").(*i18n.Locale)
	return l.String(key, args...)
}

var ManifestEntries map[string]Asset

type Asset struct {
	File string   `json:"file"`
	Css  []string `json:"css"`
}
