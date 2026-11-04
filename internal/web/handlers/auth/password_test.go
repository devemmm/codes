package auth_test

import (
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"sync"
	"testing"

	"github.com/devemmm/codes/internal/db"
	webtest "github.com/devemmm/codes/internal/web/test"
	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/require"
)

func TestRegisterPage(t *testing.T) {
	s := webtest.Setup(t)
	defer webtest.Teardown(t)
	s.Register(t, "thomas")
	require.NoError(t, db.UpdateSetting(db.SettingDisableSignup, "0"))

	t.Run("Form", func(t *testing.T) {
		s.Request(t, "GET", "/-/register", nil, 200)
		s.TestCtxData(t, echo.Map{
			"isLoginPage":   false,
			"disableForm":   false,
			"disableSignup": false,
		})
	})

	t.Run("FormDisabled", func(t *testing.T) {
		s.Login(t, "thomas")
		s.Request(t, "PUT", "/-/admin-panel/set-config", url.Values{"key": {"disable-signup"}, "value": {"1"}}, 200)
		s.Logout()

		s.Request(t, "GET", "/-/register", nil, 200)
		s.TestCtxData(t, echo.Map{
			"disableSignup": true,
		})
	})

	t.Run("FormDisabledWithInviteCode", func(t *testing.T) {
		s.Login(t, "thomas")
		s.Request(t, "PUT", "/-/admin-panel/set-config", url.Values{"key": {"disable-signup"}, "value": {"1"}}, 200)

		s.Request(t, "POST", "/-/admin-panel/invitations", url.Values{
			"nbMax":         {"10"},
			"expiredAtUnix": {""},
		}, 302)

		invitation, err := db.GetInvitationByID(1)
		require.NoError(t, err)

		s.Logout()

		s.Request(t, "GET", "/-/register", nil, 200)
		s.TestCtxData(t, echo.Map{
			"disableSignup": true,
		})
		s.Request(t, "GET", "/-/register?code="+invitation.Code, nil, 200)
		s.TestCtxData(t, echo.Map{
			"disableSignup": false,
		})
	})
}

func TestProcessRegister(t *testing.T) {
	s := webtest.Setup(t)
	defer webtest.Teardown(t)

	s.Register(t, "thomas")
	s.Register(t, "alice")
	require.NoError(t, db.UpdateSetting(db.SettingDisableSignup, "0"))

	t.Run("Register", func(t *testing.T) {
		user, err := db.GetUserByUsername("thomas")
		require.NoError(t, err)
		require.True(t, user.IsAdmin)
		s.Logout()

		s.Request(t, "POST", "/-/register", db.UserDTO{Username: "seconduser", Password: "password123"}, 302)
		user, err = db.GetUserByUsername("seconduser")
		require.NoError(t, err)
		require.False(t, user.IsAdmin)
		s.Logout()
	})

	t.Run("DuplicateUsername", func(t *testing.T) {
		s.Request(t, "POST", "/-/register", db.UserDTO{Username: "useraaa", Password: "password123"}, 302)
		s.Logout()
		s.Request(t, "POST", "/-/register", db.UserDTO{Username: "useraaa", Password: "password456"}, 200)
		s.Logout()
	})

	t.Run("InvalidUsername", func(t *testing.T) {
		s.Request(t, "POST", "/-/register", db.UserDTO{Username: "", Password: "password123"}, 200)
		s.Request(t, "POST", "/-/register", db.UserDTO{Username: "aze@", Password: "password123"}, 200)
		s.Request(t, "POST", "/-/register", db.UserDTO{Username: "-", Password: "password123"}, 200)

		exists, err := db.UserExists("-")
		require.NoError(t, err)
		require.False(t, exists)
	})

	t.Run("EmptyPassword", func(t *testing.T) {
		s.Request(t, "POST", "/-/register", db.UserDTO{Username: "newuser", Password: ""}, 200)
	})

	t.Run("RegisterDisabled", func(t *testing.T) {
		s.Login(t, "thomas")
		s.Request(t, "PUT", "/-/admin-panel/set-config", url.Values{"key": {"disable-signup"}, "value": {"1"}}, 200)
		s.Logout()

		s.Request(t, "POST", "/-/register", db.UserDTO{Username: "blocked", Password: "password123"}, 403)

		exists, err := db.UserExists("blocked")
		require.NoError(t, err)
		require.False(t, exists)
	})

	t.Run("RegisterWithInvitationCode", func(t *testing.T) {
		s.Login(t, "thomas")
		s.Request(t, "PUT", "/-/admin-panel/set-config", url.Values{"key": {"disable-signup"}, "value": {"1"}}, 200)
		s.Request(t, "POST", "/-/admin-panel/invitations", url.Values{
			"nbMax":         {"10"},
			"expiredAtUnix": {""},
		}, 302)
		s.Logout()

		invitations, err := db.GetAllInvitations()
		require.NoError(t, err)
		require.NotEmpty(t, invitations)
		invitation := invitations[len(invitations)-1]

		s.Logout()

		s.Request(t, "POST", "/-/register?code="+invitation.Code, db.UserDTO{Username: "inviteduser", Password: "password123"}, 302)

		user, err := db.GetUserByUsername("inviteduser")
		require.NoError(t, err)
		require.Equal(t, "inviteduser", user.Username)

		updatedInvitation, err := db.GetInvitationByID(invitation.ID)
		require.NoError(t, err)
		require.Equal(t, uint(1), updatedInvitation.NbUsed)
	})
}

func TestEmptyDatabaseRegistrationBootstrap(t *testing.T) {
	s := webtest.Setup(t)
	defer webtest.Teardown(t)

	setting, err := db.GetSetting(db.SettingDisableSignup)
	require.NoError(t, err)
	require.Equal(t, "1", setting, "new databases must default to disabled signup")

	// An invitation must not bypass the CLI-only first-admin bootstrap.
	invitation := &db.Invitation{NbMax: 1}
	require.NoError(t, invitation.Create())
	s.Request(t, "GET", "/-/register?code="+invitation.Code, nil, 200)
	s.TestCtxData(t, echo.Map{"disableSignup": true})
	s.Request(t, "POST", "/-/register?code="+invitation.Code, db.UserDTO{Username: "invited", Password: "password123"}, 403)
	exists, err := db.UserExists("invited")
	require.NoError(t, err)
	require.False(t, exists)

	// Simulate a database initialized by an older version, with signup persisted
	// as enabled but no users. Re-running setup must close that bootstrap gap.
	require.NoError(t, db.UpdateSetting(db.SettingDisableSignup, "0"))
	require.NoError(t, db.DisableSignupForEmptyDatabase())
	setting, err = db.GetSetting(db.SettingDisableSignup)
	require.NoError(t, err)
	require.Equal(t, "1", setting, "an empty legacy database must disable signup")

	// Even if an old/manual setting says signup is enabled, simultaneous web
	// requests cannot create the first account.
	require.NoError(t, db.UpdateSetting(db.SettingDisableSignup, "0"))
	base := s.StartHttpServer(t)
	const requestCount = 8
	clients := make([]*http.Client, requestCount)
	tokens := make([]string, requestCount)
	for i := range clients {
		jar, err := cookiejar.New(nil)
		require.NoError(t, err)
		clients[i] = &http.Client{Jar: jar}
		resp, err := clients[i].Get(base + "/-/register")
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
		for _, cookie := range jar.Cookies(resp.Request.URL) {
			if cookie.Name == "_csrf" {
				tokens[i] = cookie.Value
			}
		}
		require.NotEmpty(t, tokens[i], "registration response should set a CSRF cookie")
	}

	var wg sync.WaitGroup
	statuses := make(chan int, requestCount)
	for i := range clients {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			resp, err := clients[i].PostForm(base+"/-/register", url.Values{
				"_csrf":    {tokens[i]},
				"username": {fmt.Sprintf("racer%d", i)},
				"password": {"password123"},
			})
			if err != nil {
				statuses <- 0
				return
			}
			_ = resp.Body.Close()
			statuses <- resp.StatusCode
		}(i)
	}
	wg.Wait()
	close(statuses)
	for status := range statuses {
		require.Equal(t, http.StatusForbidden, status)
	}
	count, err := db.CountAll(&db.User{})
	require.NoError(t, err)
	require.Zero(t, count, "concurrent web registration must not seed any users")
}

func TestPopulatedDatabaseKeepsSignupPolicyAndAdminCanReEnableIt(t *testing.T) {
	s := webtest.Setup(t)
	defer webtest.Teardown(t)

	s.Register(t, "admin")
	admin, err := db.GetUserByUsername("admin")
	require.NoError(t, err)
	require.True(t, admin.IsAdmin)
	s.Login(t, "admin")

	// Reinitialization must preserve an existing installation's stored policy.
	require.NoError(t, db.UpdateSetting(db.SettingDisableSignup, "0"))
	require.NoError(t, db.DisableSignupForEmptyDatabase())
	setting, err := db.GetSetting(db.SettingDisableSignup)
	require.NoError(t, err)
	require.Equal(t, "0", setting)

	// The existing administrator can still control public registration through
	// the admin panel after first-user signup has been hardened.
	s.Request(t, "PUT", "/-/admin-panel/set-config", url.Values{
		"key":   {db.SettingDisableSignup},
		"value": {"1"},
	}, 200)
	s.Request(t, "PUT", "/-/admin-panel/set-config", url.Values{
		"key":   {db.SettingDisableSignup},
		"value": {"0"},
	}, 200)
	s.Request(t, "POST", "/-/register", db.UserDTO{Username: "member", Password: "password123"}, 302)
	member, err := db.GetUserByUsername("member")
	require.NoError(t, err)
	require.False(t, member.IsAdmin, "web registration must never grant admin based on user ID")

	admin, err = db.GetUserByUsername("admin")
	require.NoError(t, err)
	require.True(t, admin.IsAdmin, "existing administrator privileges must remain intact")
}

func TestLoginPage(t *testing.T) {
	s := webtest.Setup(t)
	defer webtest.Teardown(t)
	s.Register(t, "thomas")

	t.Run("Form", func(t *testing.T) {
		s.Request(t, "GET", "/-/login", nil, 200)
		s.TestCtxData(t, echo.Map{
			"isLoginPage": true,
			"disableForm": false,
		})
	})

	t.Run("FormDisabled", func(t *testing.T) {
		s.Login(t, "thomas")
		s.Request(t, "PUT", "/-/admin-panel/set-config", url.Values{"key": {"disable-login-form"}, "value": {"1"}}, 200)
		s.Logout()

		s.Request(t, "GET", "/-/login", nil, 200)
		s.TestCtxData(t, echo.Map{
			"disableForm": true,
		})
	})
}

func TestProcessLogin(t *testing.T) {
	s := webtest.Setup(t)
	defer webtest.Teardown(t)

	s.Register(t, "thomas")

	t.Run("ValidCredentials", func(t *testing.T) {
		resp := s.Request(t, "POST", "/-/login", db.UserDTO{Username: "thomas", Password: "thomas"}, 302)
		require.Equal(t, "/", resp.Header.Get("Location"))
		require.NotEmpty(t, s.SessionCookie)
		require.Equal(t, "thomas", s.User().Username)

		s.Logout()
	})

	t.Run("InvalidPassword", func(t *testing.T) {
		resp := s.Request(t, "POST", "/-/login", db.UserDTO{Username: "thomas", Password: "wrongpassword"}, 302)
		require.Equal(t, "/-/login", resp.Header.Get("Location"))
		require.Nil(t, s.User())
	})

	t.Run("NonExistentUser", func(t *testing.T) {
		resp := s.Request(t, "POST", "/-/login", db.UserDTO{Username: "nonexistent", Password: "password"}, 302)
		require.Equal(t, "/-/login", resp.Header.Get("Location"))
		require.Nil(t, s.User())
	})

	t.Run("EmptyCredentials", func(t *testing.T) {
		s.Request(t, "POST", "/-/login", db.UserDTO{Username: "", Password: ""}, 302)
		require.Nil(t, s.User())
	})

	t.Run("LoginFormDisabled", func(t *testing.T) {
		s.Login(t, "thomas")
		s.Request(t, "PUT", "/-/admin-panel/set-config", url.Values{"key": {"disable-login-form"}, "value": {"1"}}, 200)
		s.Logout()

		s.Request(t, "POST", "/-/login", db.UserDTO{Username: "thomas", Password: "thomas"}, 403)
		require.Nil(t, s.User())
	})
}

func TestLogout(t *testing.T) {
	s := webtest.Setup(t)
	defer webtest.Teardown(t)

	s.Register(t, "thomas")

	t.Run("LogoutRedirects", func(t *testing.T) {
		s.Login(t, "thomas")
		require.Equal(t, "thomas", s.User().Username)

		resp := s.Request(t, "GET", "/-/logout", nil, 302)
		require.Equal(t, "/-/all", resp.Header.Get("Location"))
		require.Nil(t, s.User())
		s.Request(t, "GET", "/", nil, 302)
	})
}
