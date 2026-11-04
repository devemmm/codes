package context

import (
	"net/http"
	"path/filepath"
	"strings"
	"sync"

	"github.com/devemmm/codes/internal/config"
	"github.com/devemmm/codes/internal/session"
	"github.com/gorilla/sessions"
	"github.com/markbates/goth/gothic"
)

var gothicStoreOnce sync.Once

type Store struct {
	sessionsPath string

	flashStore *sessions.CookieStore
	UserStore  sessions.Store
	fileStore  sessions.Store
}

func NewStore(sessionsPath string) (*Store, error) {
	s := &Store{sessionsPath: sessionsPath}
	if err := session.EnsurePrivateDirectory(sessionsPath); err != nil {
		return nil, err
	}

	flashKey, err := session.LoadOrCreateSecretKey(filepath.Join(sessionsPath, "flash-signing.key"))
	if err != nil {
		return nil, err
	}
	s.flashStore = sessions.NewCookieStore(flashKey)
	hardenCookie(s.flashStore.Options)
	encryptKey, err := session.LoadOrCreateSecretKey(filepath.Join(s.sessionsPath, "session-encrypt.key"))
	if err != nil {
		return nil, err
	}
	userStore := sessions.NewFilesystemStore(s.sessionsPath, config.SecretKey, encryptKey)
	userStore.MaxLength(10 * 1024)
	hardenCookie(userStore.Options)
	s.fileStore = userStore
	s.UserStore = &guardedSessionStore{sessionsPath: sessionsPath, inner: userStore}
	gothicStoreOnce.Do(func() {
		gothic.Store = s.UserStore
	})

	return s, nil
}

// NewStoreWithSessionStore builds the standard cookie/session configuration
// around a caller-provided session backend.
func NewStoreWithSessionStore(sessionsPath string, userStore sessions.Store) (*Store, error) {
	s, err := NewStore(sessionsPath)
	if err != nil {
		return nil, err
	}
	s.UserStore = userStore
	s.fileStore = nil
	return s, nil
}

// hardenCookie applies secure defaults to a session cookie. HttpOnly keeps the
// cookie out of reach of JavaScript (so an XSS cannot read the auth session),
// and SameSite=Lax mitigates CSRF. Secure is set only when the configured
// external URL is HTTPS, otherwise the cookie would never be sent over a
// plain-HTTP deployment and logins would silently break. It is set both ways
// because sessions.NewCookieStore defaults Secure to true: left alone, the flash
// cookie is dropped by the browser on plain HTTP and no flash message is shown.
func hardenCookie(opts *sessions.Options) {
	if opts == nil {
		return
	}
	opts.HttpOnly = true
	opts.SameSite = http.SameSiteLaxMode
	opts.Secure = strings.HasPrefix(strings.ToLower(config.C.ExternalUrl), "https://")
}
