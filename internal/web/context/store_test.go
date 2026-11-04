package context

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/devemmm/codes/internal/config"
	"github.com/gorilla/sessions"
	"github.com/stretchr/testify/require"
)

func TestHardenCookieSecureFollowsExternalUrl(t *testing.T) {
	require.NoError(t, config.InitConfig("", io.Discard))

	tests := []struct {
		externalUrl string
		secure      bool
	}{
		{"", false},
		{"http://gist.example.com", false},
		{"https://gist.example.com", true},
		{"HTTPS://gist.example.com", true},
	}
	for _, tt := range tests {
		t.Run(tt.externalUrl, func(t *testing.T) {
			config.C.ExternalUrl = tt.externalUrl

			// The flash store is a cookie store, whose default Options already set
			// Secure; a plain-HTTP deployment has to clear it.
			cookieOpts := sessions.NewCookieStore(make([]byte, 32)).Options
			hardenCookie(cookieOpts)
			require.Equal(t, tt.secure, cookieOpts.Secure)

			fsOpts := sessions.NewFilesystemStore(t.TempDir()).Options
			hardenCookie(fsOpts)
			require.Equal(t, tt.secure, fsOpts.Secure)
		})
	}
}

func TestGuardedSessionStorePreservesExistingSessionValues(t *testing.T) {
	require.NoError(t, config.InitConfig("", io.Discard))
	oldSecret := config.SecretKey
	config.SecretKey = []byte("stable-test-session-signing-key-32b")
	t.Cleanup(func() { config.SecretKey = oldSecret })
	config.C.ExternalUrl = ""

	store, err := NewStore(t.TempDir())
	require.NoError(t, err)
	firstRequest := httptest.NewRequest(http.MethodGet, "/", nil)
	firstResponse := httptest.NewRecorder()
	sess, err := store.UserStore.Get(firstRequest, "session")
	require.NoError(t, err)
	sess.Values["user"] = uint(42)
	sess.Values["oauthProvider"] = "github"
	require.NoError(t, sess.Save(firstRequest, firstResponse))
	cookies := firstResponse.Result().Cookies()
	require.NotEmpty(t, cookies)

	secondRequest := httptest.NewRequest(http.MethodGet, "/", nil)
	secondRequest.AddCookie(cookies[0])
	secondResponse := httptest.NewRecorder()
	sess, err = store.UserStore.Get(secondRequest, "session")
	require.NoError(t, err)
	require.Equal(t, uint(42), sess.Values["user"])
	require.Equal(t, "github", sess.Values["oauthProvider"])
	sess.Values["style"] = "dark"
	require.NoError(t, sess.Save(secondRequest, secondResponse))

	thirdRequest := httptest.NewRequest(http.MethodGet, "/", nil)
	thirdRequest.AddCookie(cookies[0])
	sess, err = store.UserStore.Get(thirdRequest, "session")
	require.NoError(t, err)
	require.Equal(t, uint(42), sess.Values["user"])
	require.Equal(t, "github", sess.Values["oauthProvider"])
	require.Equal(t, "dark", sess.Values["style"])
	require.True(t, strings.Contains(secondResponse.Header().Get("Set-Cookie"), "session="))
}
