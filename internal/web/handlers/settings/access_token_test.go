package settings_test

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/devemmm/codes/internal/config"
	"github.com/devemmm/codes/internal/db"
	"github.com/devemmm/codes/internal/i18n"
	"github.com/devemmm/codes/internal/session"
	webvalidator "github.com/devemmm/codes/internal/validator"
	webcontext "github.com/devemmm/codes/internal/web/context"
	settingshandler "github.com/devemmm/codes/internal/web/handlers/settings"
	webtest "github.com/devemmm/codes/internal/web/test"
	"github.com/gorilla/sessions"
	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/require"
)

var bearerTokenPattern = regexp.MustCompile(`og_[a-f0-9]{64}`)

type persistenceFailure int

const (
	persistNormally persistenceFailure = iota
	failBeforePersist
	persistThenFail
)

type injectableSessionStore struct {
	mu     sync.Mutex
	values map[interface{}]interface{}
	fail   persistenceFailure
}

func (s *injectableSessionStore) Get(r *http.Request, name string) (*sessions.Session, error) {
	return sessions.GetRegistry(r).Get(s, name)
}

func (s *injectableSessionStore) New(_ *http.Request, name string) (*sessions.Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	values := make(map[interface{}]interface{}, len(s.values))
	for key, value := range s.values {
		values[key] = value
	}
	sess := sessions.NewSession(s, name)
	sess.ID = "injected-session-id"
	sess.Values = values
	sess.Options = &sessions.Options{Path: "/", MaxAge: 3600}
	return sess, nil
}

func (s *injectableSessionStore) Save(_ *http.Request, _ http.ResponseWriter, sess *sessions.Session) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fail == failBeforePersist {
		return errors.New("injected save failure before write")
	}
	values := make(map[interface{}]interface{}, len(sess.Values))
	for key, value := range sess.Values {
		values[key] = value
	}
	s.values = values
	if s.fail == persistThenFail {
		return errors.New("injected save failure after write")
	}
	return nil
}

type tokenTestRenderer struct{}

func (tokenTestRenderer) Render(w io.Writer, _ string, data interface{}, _ echo.Context) error {
	if values, ok := data.(map[string]interface{}); ok {
		if token, ok := values["newAccessToken"].(string); ok {
			_, err := io.WriteString(w, token)
			return err
		}
	}
	return nil
}

func serveAccessTokenHandler(t *testing.T, user *db.User, store *injectableSessionStore, method string, body url.Values, consume bool) *httptest.ResponseRecorder {
	t.Helper()
	storePath := t.TempDir()
	ctxStore, err := webcontext.NewStoreWithSessionStore(storePath, store)
	require.NoError(t, err)
	e := echo.New()
	e.Validator = webvalidator.NewValidator()
	e.Renderer = tokenTestRenderer{}
	e.HTTPErrorHandler = func(err error, c echo.Context) {
		var webErr *webcontext.HTTPError
		if errors.As(err, &webErr) {
			_ = c.String(webErr.Code, fmt.Sprint(webErr.Message))
			return
		}
		e.DefaultHTTPErrorHandler(err, c)
	}
	e.Any("/tokens", func(c echo.Context) error {
		ctx := webcontext.NewContextWithStore(c, ctxStore)
		ctx.User = user
		locale, localeErr := i18n.Locales.GetLocale("en-US")
		if localeErr != nil {
			return localeErr
		}
		ctx.SetData("locale", locale)
		ctx.SetData("c", config.C)
		if consume {
			return settingshandler.AccessTokens(ctx)
		}
		return settingshandler.AccessTokensProcess(ctx)
	})
	request := httptest.NewRequest(method, "/tokens", strings.NewReader(body.Encode()))
	if method == http.MethodPost {
		request.Header.Set(echo.HeaderContentType, echo.MIMEApplicationForm)
	}
	response := httptest.NewRecorder()
	e.ServeHTTP(response, request)
	return response
}

func assertNoBearerTokenInFailureResponse(t *testing.T, response *httptest.ResponseRecorder) {
	t.Helper()
	require.Equal(t, http.StatusInternalServerError, response.Code)
	require.Empty(t, response.Header().Get("Location"))
	require.Empty(t, bearerTokenPattern.Find(response.Body.Bytes()))
	for key, values := range response.Header() {
		for _, value := range values {
			require.Emptyf(t, bearerTokenPattern.FindString(value), "token found in response header %q", key)
		}
	}
}

func TestAccessTokenCreatePersistenceFailures(t *testing.T) {
	for _, mode := range []persistenceFailure{failBeforePersist, persistThenFail} {
		t.Run(fmt.Sprint(mode), func(t *testing.T) {
			s := webtest.Setup(t)
			defer webtest.Teardown(t)
			s.Register(t, "persist-fail")
			user, err := db.GetUserById(1)
			require.NoError(t, err)
			backend := &injectableSessionStore{values: map[interface{}]interface{}{"user": user.ID}, fail: mode}
			response := serveAccessTokenHandler(t, user, backend, http.MethodPost, url.Values{
				"name": {"persistence-failure"}, "scope_gist": {"1"},
			}, false)
			assertNoBearerTokenInFailureResponse(t, response)
			tokens, err := db.GetAccessTokensByUserID(user.ID)
			require.NoError(t, err)
			require.Empty(t, tokens, "failed session persistence must clean up the database token")

			if mode == persistThenFail {
				pendingRaw, _ := backend.values["pending-access-token-value"].(string)
				require.NotEmpty(t, pendingRaw, "the fake should model a write followed by an error")
				backend.fail = persistNormally
				followUp := serveAccessTokenHandler(t, user, backend, http.MethodGet, nil, true)
				require.Equal(t, http.StatusOK, followUp.Code)
				require.NotContains(t, followUp.Body.String(), pendingRaw, "a stale pending token without a database row must not be displayed")
			}
		})
	}
}

func TestAccessTokenConsumePersistenceFailures(t *testing.T) {
	for _, mode := range []persistenceFailure{failBeforePersist, persistThenFail} {
		t.Run(fmt.Sprint(mode), func(t *testing.T) {
			s := webtest.Setup(t)
			defer webtest.Teardown(t)
			s.Register(t, "consume-fail")
			user, err := db.GetUserById(1)
			require.NoError(t, err)
			token := &db.AccessToken{Name: "consume-failure", UserID: user.ID, ScopeGist: db.ReadPermission}
			raw, err := token.GenerateToken()
			require.NoError(t, err)
			require.NoError(t, token.Create())
			backend := &injectableSessionStore{
				values: map[interface{}]interface{}{
					"user":                         user.ID,
					"pending-access-token-value":   raw,
					"pending-access-token-expires": time.Now().Add(time.Minute).Unix(),
					"pending-access-token-id":      token.ID,
				},
				fail: mode,
			}
			response := serveAccessTokenHandler(t, user, backend, http.MethodGet, nil, true)
			assertNoBearerTokenInFailureResponse(t, response)
			_, err = db.GetAccessTokenByID(token.ID)
			require.Error(t, err, "failed token consumption should revoke the undisplayed token")
			require.NotContains(t, response.Body.String(), raw)
		})
	}
}

func TestAccessTokenIsDisplayedOnceAndNeverInCookie(t *testing.T) {
	s := webtest.Setup(t)
	defer webtest.Teardown(t)
	s.Register(t, "thomas")
	s.Login(t, "thomas")

	create := s.Request(t, http.MethodPost, "/-/settings/access-tokens", db.AccessTokenDTO{
		Name: "one-time", ScopeGist: db.ReadPermission,
	}, 302)
	defer create.Body.Close()
	createBytes, err := io.ReadAll(create.Body)
	require.NoError(t, err)
	require.Empty(t, bearerTokenPattern.Find(createBytes), "POST response body must not contain the raw token")
	for _, cookie := range create.Cookies() {
		require.NotContains(t, cookie.String(), "og_", "cookies must never contain a bearer token")
	}

	first := s.Request(t, http.MethodGet, "/-/settings/access-tokens", nil, 200)
	firstBytes, err := io.ReadAll(first.Body)
	require.NoError(t, err)
	require.NoError(t, first.Body.Close())
	tokens := bearerTokenPattern.FindAllString(string(firstBytes), -1)
	require.Len(t, tokens, 1)
	require.Equal(t, "no-store", first.Header.Get("Cache-Control"))
	require.NotContains(t, first.Header.Get("Set-Cookie"), tokens[0])

	second := s.Request(t, http.MethodGet, "/-/settings/access-tokens", nil, 200)
	secondBytes, err := io.ReadAll(second.Body)
	require.NoError(t, err)
	require.NoError(t, second.Body.Close())
	require.NotContains(t, string(secondBytes), tokens[0], "a consumed token must not be displayed again")
}

func TestAccessTokenConcurrentDisplayIsOneTime(t *testing.T) {
	s := webtest.Setup(t)
	defer webtest.Teardown(t)
	s.Register(t, "thomas")
	s.Login(t, "thomas")
	s.Request(t, http.MethodPost, "/-/settings/access-tokens", db.AccessTokenDTO{
		Name: "concurrent", ScopeGist: db.ReadPermission,
	}, 302)

	const requests = 8
	baseURL := s.StartHttpServer(t)
	sessionCookie := s.SessionCookie
	var wg sync.WaitGroup
	type result struct {
		body string
		err  error
	}
	results := make(chan result, requests)
	for i := 0; i < requests; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req, err := http.NewRequest(http.MethodGet, baseURL+"/-/settings/access-tokens", nil)
			if err != nil {
				results <- result{err: err}
				return
			}
			req.AddCookie(&http.Cookie{Name: "session", Value: sessionCookie})
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				results <- result{err: err}
				return
			}
			body, err := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			results <- result{body: string(body), err: err}
		}()
	}
	wg.Wait()
	close(results)
	shown := map[string]int{}
	for result := range results {
		require.NoError(t, result.err)
		if token := bearerTokenPattern.FindString(result.body); token != "" {
			shown[token]++
		}
	}
	require.Len(t, shown, 1, "exactly one concurrent request may display the raw token")
	for _, count := range shown {
		require.Equal(t, 1, count, "a displayed token must not appear in more than one response")
	}
}

func TestAccessTokenDisplayExpires(t *testing.T) {
	s := webtest.Setup(t)
	defer webtest.Teardown(t)
	s.Register(t, "thomas")
	s.Login(t, "thomas")
	s.Request(t, http.MethodPost, "/-/settings/access-tokens", db.AccessTokenDTO{
		Name: "expired-display", ScopeGist: db.ReadPermission,
	}, 302)

	// Expire the pending value in the encrypted filesystem session.
	sessionPath := filepath.Join(config.GetHomeDir(), "sessions")
	encKey, generated := session.GenerateSecretKey(filepath.Join(sessionPath, "session-encrypt.key"))
	require.False(t, generated)
	store := sessions.NewFilesystemStore(sessionPath, config.SecretKey, encKey)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: s.SessionCookie})
	sess, err := store.Get(req, "session")
	require.NoError(t, err)
	sess.Values["pending-access-token-expires"] = time.Now().Add(-time.Second).Unix()
	w := httptest.NewRecorder()
	require.NoError(t, sess.Save(req, w))
	for _, cookie := range w.Result().Cookies() {
		if cookie.Name == "session" {
			s.SessionCookie = cookie.Value
		}
	}

	response := s.Request(t, http.MethodGet, "/-/settings/access-tokens", nil, 200)
	body, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())
	require.Empty(t, bearerTokenPattern.Find(body))
	tokens, err := db.GetAccessTokensByUserID(1)
	require.NoError(t, err)
	require.Empty(t, tokens, "an undisplayed expired token should be revoked")
}

func TestAccessTokensCRUD(t *testing.T) {
	s := webtest.Setup(t)
	defer webtest.Teardown(t)

	s.Register(t, "thomas")

	t.Run("RequiresAuth", func(t *testing.T) {
		s.Logout()
		s.Request(t, "GET", "/-/settings/access-tokens", nil, 302)
	})

	t.Run("AccessTokensPage", func(t *testing.T) {
		s.Login(t, "thomas")
		s.Request(t, "GET", "/-/settings/access-tokens", nil, 200)
	})

	t.Run("CreateReadToken", func(t *testing.T) {
		s.Login(t, "thomas")
		s.Request(t, "POST", "/-/settings/access-tokens", db.AccessTokenDTO{
			Name:      "test-token",
			ScopeGist: db.ReadPermission,
		}, 302)

		tokens, err := db.GetAccessTokensByUserID(1)
		require.NoError(t, err)
		require.Len(t, tokens, 1)
		require.Equal(t, "test-token", tokens[0].Name)
		require.Equal(t, uint(db.ReadPermission), tokens[0].ScopeGist)
		require.Equal(t, int64(0), tokens[0].ExpiresAt)
		s.Request(t, "GET", "/-/settings/access-tokens", nil, 200)
	})

	t.Run("CreateExpiringToken", func(t *testing.T) {
		s.Login(t, "thomas")
		tomorrow := time.Now().AddDate(0, 0, 1).Format("2006-01-02")
		s.Request(t, "POST", "/-/settings/access-tokens", db.AccessTokenDTO{
			Name:      "expiring-token",
			ScopeGist: db.ReadWritePermission,
			ExpiresAt: tomorrow,
		}, 302)

		tokens, err := db.GetAccessTokensByUserID(1)
		require.NoError(t, err)
		require.Len(t, tokens, 2)
	})

	t.Run("DeleteToken", func(t *testing.T) {
		s.Login(t, "thomas")
		s.Request(t, "DELETE", "/-/settings/access-tokens/1", nil, 302)

		tokens, err := db.GetAccessTokensByUserID(1)
		require.NoError(t, err)
		require.Len(t, tokens, 1)
		require.Equal(t, "expiring-token", tokens[0].Name)
	})
}

func TestAccessTokenPrivateGistAccess(t *testing.T) {
	s := webtest.Setup(t)
	defer webtest.Teardown(t)

	s.Register(t, "thomas")
	_, _, user, identifier := s.CreateGist(t, "2")

	// Create access token with read permission
	token := &db.AccessToken{
		Name:      "read-token",
		UserID:    1,
		ScopeGist: db.ReadPermission,
	}
	plainToken, err := token.GenerateToken()
	require.NoError(t, err)
	require.NoError(t, token.Create())

	s.Logout()
	headers := map[string]string{"Authorization": "Token " + plainToken}

	t.Run("NoTokenReturns404", func(t *testing.T) {
		s.Request(t, "GET", "/"+user+"/"+identifier, nil, 404)
	})

	t.Run("ValidTokenGrantsAccess", func(t *testing.T) {
		s.RequestWithHeaders(t, "GET", "/"+user+"/"+identifier, nil, 200, headers)
	})

	t.Run("RawContentAccessible", func(t *testing.T) {
		s.RequestWithHeaders(t, "GET", "/"+user+"/"+identifier+"/raw/HEAD/file.txt", nil, 200, headers)
	})

	t.Run("JSONEndpointAccessible", func(t *testing.T) {
		s.RequestWithHeaders(t, "GET", "/"+user+"/"+identifier+".json", nil, 200, headers)
	})

	t.Run("InvalidTokenReturns404", func(t *testing.T) {
		s.RequestWithHeaders(t, "GET", "/"+user+"/"+identifier, nil, 404, map[string]string{
			"Authorization": "Token invalid_token",
		})
	})
}

func TestAccessTokenPermissions(t *testing.T) {
	s := webtest.Setup(t)
	defer webtest.Teardown(t)

	s.Register(t, "thomas")
	_, _, user, identifier := s.CreateGist(t, "2")

	// Create token with NO permission
	noPermToken := &db.AccessToken{
		Name:      "no-perm-token",
		UserID:    1,
		ScopeGist: db.NoPermission,
	}
	noPermPlain, err := noPermToken.GenerateToken()
	require.NoError(t, err)
	require.NoError(t, noPermToken.Create())

	// Create token with READ permission
	readToken := &db.AccessToken{
		Name:      "read-token",
		UserID:    1,
		ScopeGist: db.ReadPermission,
	}
	readPlain, err := readToken.GenerateToken()
	require.NoError(t, err)
	require.NoError(t, readToken.Create())

	s.Logout()

	t.Run("NoPermissionDenied", func(t *testing.T) {
		s.RequestWithHeaders(t, "GET", "/"+user+"/"+identifier, nil, 404, map[string]string{
			"Authorization": "Token " + noPermPlain,
		})
	})

	t.Run("ReadPermissionGranted", func(t *testing.T) {
		s.RequestWithHeaders(t, "GET", "/"+user+"/"+identifier, nil, 200, map[string]string{
			"Authorization": "Token " + readPlain,
		})
	})
}

func TestAccessTokenExpiration(t *testing.T) {
	s := webtest.Setup(t)
	defer webtest.Teardown(t)

	s.Register(t, "thomas")
	_, _, user, identifier := s.CreateGist(t, "2")

	// Create an expired token
	expiredToken := &db.AccessToken{
		Name:      "expired-token",
		UserID:    1,
		ScopeGist: db.ReadPermission,
		ExpiresAt: time.Now().Add(-24 * time.Hour).Unix(),
	}
	expiredPlain, err := expiredToken.GenerateToken()
	require.NoError(t, err)
	require.NoError(t, expiredToken.Create())

	// Create a valid token
	validToken := &db.AccessToken{
		Name:      "valid-token",
		UserID:    1,
		ScopeGist: db.ReadPermission,
		ExpiresAt: time.Now().Add(24 * time.Hour).Unix(),
	}
	validPlain, err := validToken.GenerateToken()
	require.NoError(t, err)
	require.NoError(t, validToken.Create())

	s.Logout()

	t.Run("ExpiredTokenDenied", func(t *testing.T) {
		s.RequestWithHeaders(t, "GET", "/"+user+"/"+identifier, nil, 404, map[string]string{
			"Authorization": "Token " + expiredPlain,
		})
	})

	t.Run("ValidTokenGranted", func(t *testing.T) {
		s.RequestWithHeaders(t, "GET", "/"+user+"/"+identifier, nil, 200, map[string]string{
			"Authorization": "Token " + validPlain,
		})
	})
}

func TestAccessTokenWrongUser(t *testing.T) {
	s := webtest.Setup(t)
	defer webtest.Teardown(t)

	s.Register(t, "thomas")
	s.Register(t, "kaguya")

	_, _, user, identifier := s.CreateGist(t, "2")

	// Create token for kaguya
	kaguyaToken := &db.AccessToken{
		Name:      "kaguya-token",
		UserID:    2,
		ScopeGist: db.ReadPermission,
	}
	kaguyaPlain, err := kaguyaToken.GenerateToken()
	require.NoError(t, err)
	require.NoError(t, kaguyaToken.Create())

	// Create token for thomas
	thomasToken := &db.AccessToken{
		Name:      "thomas-token",
		UserID:    1,
		ScopeGist: db.ReadPermission,
	}
	thomasPlain, err := thomasToken.GenerateToken()
	require.NoError(t, err)
	require.NoError(t, thomasToken.Create())

	s.Logout()

	t.Run("OtherUserTokenDenied", func(t *testing.T) {
		s.RequestWithHeaders(t, "GET", "/"+user+"/"+identifier, nil, 404, map[string]string{
			"Authorization": "Token " + kaguyaPlain,
		})
	})

	t.Run("OwnerTokenGranted", func(t *testing.T) {
		s.RequestWithHeaders(t, "GET", "/"+user+"/"+identifier, nil, 200, map[string]string{
			"Authorization": "Token " + thomasPlain,
		})
	})
}

func TestAccessTokenLastUsedUpdate(t *testing.T) {
	s := webtest.Setup(t)
	defer webtest.Teardown(t)

	s.Register(t, "thomas")
	_, _, user, identifier := s.CreateGist(t, "2")

	token := &db.AccessToken{
		Name:      "test-token",
		UserID:    1,
		ScopeGist: db.ReadPermission,
	}
	plainToken, err := token.GenerateToken()
	require.NoError(t, err)
	require.NoError(t, token.Create())

	// Verify LastUsedAt is 0 initially
	tokenFromDB, err := db.GetAccessTokenByID(token.ID)
	require.NoError(t, err)
	require.Equal(t, int64(0), tokenFromDB.LastUsedAt)

	s.Logout()

	// Use the token
	s.RequestWithHeaders(t, "GET", "/"+user+"/"+identifier, nil, 200, map[string]string{
		"Authorization": "Token " + plainToken,
	})

	// Verify LastUsedAt was updated
	tokenFromDB, err = db.GetAccessTokenByID(token.ID)
	require.NoError(t, err)
	require.NotEqual(t, int64(0), tokenFromDB.LastUsedAt)
}

func TestCreateTokenWithUserScope(t *testing.T) {
	s := webtest.Setup(t)
	defer webtest.Teardown(t)
	s.Register(t, "thomas")
	s.Login(t, "thomas")

	s.Request(t, "POST", "/-/settings/access-tokens", db.AccessTokenDTO{
		Name:      "with-user",
		ScopeGist: db.ReadPermission,
		ScopeUser: db.ReadPermission,
	}, 302)

	tokens, err := db.GetAccessTokensByUserID(1)
	require.NoError(t, err)
	require.Len(t, tokens, 1)
	require.Equal(t, uint(1), tokens[0].ScopeUser)
}

func TestAccessTokenWithRequireLogin(t *testing.T) {
	s := webtest.Setup(t)
	defer webtest.Teardown(t)

	s.Register(t, "thomas")
	_, _, user1, identifier1 := s.CreateGist(t, "2")

	s.Login(t, "thomas")
	_, _, user2, identifier2 := s.CreateGist(t, "0")

	s.Login(t, "thomas")
	token := &db.AccessToken{
		Name:      "read-token",
		UserID:    1,
		ScopeGist: db.ReadPermission,
	}
	plainToken, err := token.GenerateToken()
	require.NoError(t, err)
	require.NoError(t, token.Create())

	s.Request(t, "PUT", "/-/admin-panel/set-config", url.Values{"key": {db.SettingRequireLogin}, "value": {"1"}}, 200)
	s.Logout()

	headers := map[string]string{"Authorization": "Token " + plainToken}

	t.Run("UnauthenticatedRedirects", func(t *testing.T) {
		s.Request(t, "GET", "/"+user1+"/"+identifier1, nil, 302)
		s.Request(t, "GET", "/"+user2+"/"+identifier2, nil, 302)
	})

	t.Run("ValidTokenGrantsAccess", func(t *testing.T) {
		s.RequestWithHeaders(t, "GET", "/"+user1+"/"+identifier1, nil, 200, headers)
		s.RequestWithHeaders(t, "GET", "/"+user2+"/"+identifier2, nil, 200, headers)
		s.RequestWithHeaders(t, "GET", "/"+user1+"/"+identifier1+"/raw/HEAD/file.txt", nil, 200, headers)
	})

	t.Run("InvalidTokenRedirects", func(t *testing.T) {
		s.RequestWithHeaders(t, "GET", "/"+user1+"/"+identifier1, nil, 302, map[string]string{
			"Authorization": "Token invalid_token",
		})
	})

	t.Run("NoPermTokenRedirects", func(t *testing.T) {
		noPermToken := &db.AccessToken{
			Name:      "no-perm-token",
			UserID:    1,
			ScopeGist: db.NoPermission,
		}
		noPermPlain, err := noPermToken.GenerateToken()
		require.NoError(t, err)
		require.NoError(t, noPermToken.Create())

		s.RequestWithHeaders(t, "GET", "/"+user1+"/"+identifier1, nil, 302, map[string]string{
			"Authorization": "Token " + noPermPlain,
		})
	})
}
