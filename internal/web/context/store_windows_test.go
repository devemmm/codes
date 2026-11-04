//go:build windows

package context

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/devemmm/codes/internal/config"
	"github.com/devemmm/codes/internal/session"
	"github.com/stretchr/testify/require"
)

func TestWindowsFilesystemSessionsWorkOnNTFS(t *testing.T) {
	oldSecret := config.SecretKey
	t.Cleanup(func() { config.SecretKey = oldSecret })
	require.NoError(t, config.InitConfig("", io.Discard))
	config.SecretKey = []byte("stable-test-session-signing-key-32b")

	store, err := NewStore(filepath.Join(t.TempDir(), "sessions"))
	if err != nil {
		if errors.Is(err, session.ErrSecureSessionsUnsupported) {
			t.Skip("Windows runtime test requires NTFS")
		}
		require.NoError(t, err)
	}
	firstRequest := httptest.NewRequest(http.MethodGet, "/", nil)
	firstResponse := httptest.NewRecorder()
	sess, err := store.UserStore.Get(firstRequest, "session")
	require.NoError(t, err)
	sess.Values["user"] = uint(31)
	require.NoError(t, sess.Save(firstRequest, firstResponse))
	require.NotEmpty(t, firstResponse.Result().Cookies())

	secondRequest := httptest.NewRequest(http.MethodGet, "/", nil)
	secondRequest.AddCookie(firstResponse.Result().Cookies()[0])
	sess, err = store.UserStore.Get(secondRequest, "session")
	require.NoError(t, err)
	require.Equal(t, uint(31), sess.Values["user"])
}
