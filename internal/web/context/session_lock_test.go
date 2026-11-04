package context

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/devemmm/codes/internal/config"
	"github.com/gorilla/sessions"
	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/require"
)

func TestSessionLockSerializesSeparateProcesses(t *testing.T) {
	dir := t.TempDir()
	const workers = 5
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cmd := exec.Command(os.Args[0], "-test.run=^TestSessionLockSubprocessHelper$")
			cmd.Env = append(os.Environ(), "CODES_SESSION_LOCK_HELPER=1", "CODES_SESSION_LOCK_DIR="+dir)
			if output, err := cmd.CombinedOutput(); err != nil {
				errs <- errors.New(string(output) + err.Error())
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
}

func TestSessionLockSubprocessHelper(t *testing.T) {
	if os.Getenv("CODES_SESSION_LOCK_HELPER") != "1" {
		return
	}
	dir := os.Getenv("CODES_SESSION_LOCK_DIR")
	s := &Store{sessionsPath: dir}
	err := s.withSessionLock("session-shared", func() error {
		marker := filepath.Join(dir, "active")
		f, err := os.OpenFile(marker, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		_ = f.Close()
		time.Sleep(40 * time.Millisecond)
		return os.Remove(marker)
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestSessionLocksDoNotSerializeDifferentShards(t *testing.T) {
	s := &Store{sessionsPath: t.TempDir()}
	firstID, secondID := "session-one", "session-two"
	for lockShard(firstID) == lockShard(secondID) {
		secondID += "x"
	}
	firstEntered := make(chan struct{})
	releaseFirst := make(chan struct{})
	firstDone := make(chan error, 1)
	go func() {
		firstDone <- s.withSessionLock(firstID, func() error {
			close(firstEntered)
			<-releaseFirst
			return nil
		})
	}()
	<-firstEntered
	secondEntered := make(chan struct{})
	secondDone := make(chan error, 1)
	go func() {
		secondDone <- s.withSessionLock(secondID, func() error {
			close(secondEntered)
			return nil
		})
	}()
	select {
	case <-secondEntered:
	case <-time.After(time.Second):
		close(releaseFirst)
		t.Fatal("different session shards were serialized")
	}
	close(releaseFirst)
	require.NoError(t, <-firstDone)
	require.NoError(t, <-secondDone)
}

func lockShard(sessionID string) byte {
	hash := sha256.Sum256([]byte(sessionID))
	return hash[0]
}

type failingSessionStore struct{ err error }

type memorySessionStore struct {
	mu        sync.Mutex
	sessionID string
	values    map[interface{}]interface{}
	exists    bool
}

func (s *memorySessionStore) Get(r *http.Request, name string) (*sessions.Session, error) {
	return sessions.GetRegistry(r).Get(s, name)
}
func (s *memorySessionStore) New(r *http.Request, name string) (*sessions.Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess := sessions.NewSession(s, name)
	sess.ID = s.sessionID
	values, err := cloneSessionValues(s.values)
	if err != nil {
		return nil, err
	}
	sess.Values = values
	sess.Options = &sessions.Options{Path: "/", MaxAge: 3600}
	sess.IsNew = !s.exists
	return sess, nil
}
func (s *memorySessionStore) Save(r *http.Request, w http.ResponseWriter, sess *sessions.Session) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if sess.Options.MaxAge <= 0 {
		s.exists = false
		s.values = nil
		return nil
	}
	values, err := cloneSessionValues(sess.Values)
	if err != nil {
		return err
	}
	s.values = values
	s.exists = true
	return nil
}

func TestOrdinarySessionSavesMergeIndependentConcurrentUpdates(t *testing.T) {
	inner := &memorySessionStore{sessionID: "session-merge", exists: true, values: map[interface{}]interface{}{"user": uint(17)}}
	store := &guardedSessionStore{sessionsPath: t.TempDir(), inner: inner}
	req1 := httptest.NewRequest(http.MethodGet, "/", nil)
	req2 := httptest.NewRequest(http.MethodGet, "/", nil)
	first, err := store.Get(req1, "session")
	require.NoError(t, err)
	second, err := store.Get(req2, "session")
	require.NoError(t, err)
	first.Values["style"] = "dark"
	second.Values["oauth_state"] = "state-123"
	require.NoError(t, first.Save(req1, httptest.NewRecorder()))
	require.NoError(t, second.Save(req2, httptest.NewRecorder()))
	inner.mu.Lock()
	defer inner.mu.Unlock()
	require.Equal(t, uint(17), inner.values["user"])
	require.Equal(t, "dark", inner.values["style"])
	require.Equal(t, "state-123", inner.values["oauth_state"])
}

func TestOrdinarySessionSaveRejectsConflictingKeyUpdates(t *testing.T) {
	inner := &memorySessionStore{sessionID: "session-conflict", exists: true, values: map[interface{}]interface{}{"mfaID": uint(17)}}
	store := &guardedSessionStore{sessionsPath: t.TempDir(), inner: inner}
	req1 := httptest.NewRequest(http.MethodGet, "/", nil)
	req2 := httptest.NewRequest(http.MethodGet, "/", nil)
	first, err := store.Get(req1, "session")
	require.NoError(t, err)
	second, err := store.Get(req2, "session")
	require.NoError(t, err)
	first.Values["mfaID"] = uint(18)
	second.Values["mfaID"] = uint(19)
	require.NoError(t, first.Save(req1, httptest.NewRecorder()))
	require.ErrorIs(t, second.Save(req2, httptest.NewRecorder()), ErrSessionConflict)
	inner.mu.Lock()
	defer inner.mu.Unlock()
	require.Equal(t, uint(18), inner.values["mfaID"])
}

func TestOrdinarySessionSaveRejectsExpiredSession(t *testing.T) {
	inner := &memorySessionStore{sessionID: "session-expired", exists: true, values: map[interface{}]interface{}{"user": uint(17)}}
	store := &guardedSessionStore{sessionsPath: t.TempDir(), inner: inner}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	sess, err := store.Get(req, "session")
	require.NoError(t, err)
	sess.Values["style"] = "dark"
	inner.mu.Lock()
	inner.exists = false
	inner.values = nil
	inner.mu.Unlock()
	require.ErrorIs(t, sess.Save(req, httptest.NewRecorder()), ErrSessionExpired)
}

func TestConcurrentMFAAndOAuthUpdatesPreserveSessionExpiry(t *testing.T) {
	inner := &memorySessionStore{sessionID: "session-mfa", exists: true, values: map[interface{}]interface{}{"user": uint(17)}}
	store := &guardedSessionStore{sessionsPath: t.TempDir(), inner: inner}
	reqMFA := httptest.NewRequest(http.MethodGet, "/", nil)
	reqOAuth := httptest.NewRequest(http.MethodGet, "/", nil)
	mfaSession, err := store.Get(reqMFA, "session")
	require.NoError(t, err)
	oauthSession, err := store.Get(reqOAuth, "session")
	require.NoError(t, err)
	mfaSession.Values["mfaID"] = uint(17)
	mfaSession.Options.MaxAge = 5 * 60
	oauthSession.Values["oauth_state"] = "state-123"
	require.NoError(t, mfaSession.Save(reqMFA, httptest.NewRecorder()))
	require.NoError(t, oauthSession.Save(reqOAuth, httptest.NewRecorder()))
	inner.mu.Lock()
	defer inner.mu.Unlock()
	require.Equal(t, uint(17), inner.values["mfaID"])
	require.Equal(t, "state-123", inner.values["oauth_state"])
	require.Equal(t, 5*60, inner.values[sessionMaxAgeKey])
}

func TestSessionSaveErrorMapsConflictsToConflictResponse(t *testing.T) {
	ctx := NewContextWithStore(echo.New().NewContext(
		httptest.NewRequest(http.MethodGet, "/", nil), httptest.NewRecorder()), &Store{})
	err := ctx.SaveSessionError("cannot persist session", ErrSessionConflict)
	var httpErr *HTTPError
	require.ErrorAs(t, err, &httpErr)
	require.Equal(t, http.StatusConflict, httpErr.Code)
	err = ctx.SaveSessionError("cannot persist session", errors.New("disk full"))
	require.ErrorAs(t, err, &httpErr)
	require.Equal(t, http.StatusInternalServerError, httpErr.Code)
}

func TestSessionSnapshotsCopyMutableValues(t *testing.T) {
	original := map[interface{}]interface{}{"webauthn": []byte("before")}
	snapshot, err := cloneSessionValues(original)
	require.NoError(t, err)
	original["webauthn"].([]byte)[0] = 'a'
	require.Equal(t, []byte("before"), snapshot["webauthn"])
}

func TestConcurrentSessionUpdatesMergeAcrossProcesses(t *testing.T) {
	require.NoError(t, config.InitConfig("", io.Discard))
	oldSecret := config.SecretKey
	config.SecretKey = []byte("stable-test-session-signing-key-32b")
	t.Cleanup(func() { config.SecretKey = oldSecret })
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Skipf("secure session filesystem unavailable: %v", err)
	}
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	response := httptest.NewRecorder()
	sess, err := store.UserStore.Get(request, "session")
	require.NoError(t, err)
	sess.Values["user"] = uint(42)
	require.NoError(t, sess.Save(request, response))
	cookies := response.Result().Cookies()
	require.NotEmpty(t, cookies)

	readyDir := filepath.Join(dir, "ready")
	require.NoError(t, os.Mkdir(readyDir, 0700))
	releasePath := filepath.Join(dir, "release")
	type process struct {
		cmd    *exec.Cmd
		output *bytes.Buffer
	}
	commands := make([]process, 0, 2)
	for _, update := range []string{"webauthn_registration_session", "oauth_state"} {
		cmd := exec.Command(os.Args[0], "-test.run=^TestSessionMergeSubprocessHelper$")
		cmd.Env = append(os.Environ(),
			"CODES_SESSION_MERGE_HELPER=1",
			"CODES_SESSION_MERGE_DIR="+dir,
			"CODES_SESSION_MERGE_COOKIE="+cookies[0].Value,
			"CODES_SESSION_MERGE_KEY="+update,
			"CODES_SESSION_MERGE_READY="+filepath.Join(readyDir, update),
			"CODES_SESSION_MERGE_RELEASE="+releasePath,
		)
		output := new(bytes.Buffer)
		cmd.Stdout = output
		cmd.Stderr = output
		require.NoError(t, cmd.Start())
		commands = append(commands, process{cmd: cmd, output: output})
	}
	deadline := time.Now().Add(10 * time.Second)
	for _, key := range []string{"webauthn_registration_session", "oauth_state"} {
		for {
			if _, err := os.Stat(filepath.Join(readyDir, key)); err == nil {
				break
			}
			require.Less(t, time.Now(), deadline, "subprocess did not load baseline for %s", key)
			time.Sleep(10 * time.Millisecond)
		}
	}
	require.NoError(t, os.WriteFile(releasePath, []byte("go"), 0600))
	for _, process := range commands {
		err := process.cmd.Wait()
		require.NoError(t, err, process.output.String())
	}

	verifyRequest := httptest.NewRequest(http.MethodGet, "/", nil)
	verifyRequest.AddCookie(cookies[0])
	verify, err := store.UserStore.Get(verifyRequest, "session")
	require.NoError(t, err)
	require.Equal(t, uint(42), verify.Values["user"])
	require.Equal(t, "webauthn_registration_session", verify.Values["webauthn_registration_session"])
	require.Equal(t, "oauth_state", verify.Values["oauth_state"])
}

func TestSessionMergeSubprocessHelper(t *testing.T) {
	if os.Getenv("CODES_SESSION_MERGE_HELPER") != "1" {
		return
	}
	require.NoError(t, config.InitConfig("", io.Discard))
	config.SecretKey = []byte("stable-test-session-signing-key-32b")
	store, err := NewStore(os.Getenv("CODES_SESSION_MERGE_DIR"))
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.AddCookie(&http.Cookie{Name: "session", Value: os.Getenv("CODES_SESSION_MERGE_COOKIE")})
	sess, err := store.UserStore.Get(request, "session")
	if err != nil {
		t.Fatal(err)
	}
	key := os.Getenv("CODES_SESSION_MERGE_KEY")
	sess.Values[key] = key
	if err := os.WriteFile(os.Getenv("CODES_SESSION_MERGE_READY"), []byte("ready"), 0600); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(os.Getenv("CODES_SESSION_MERGE_RELEASE")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for merge release")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := sess.Save(request, httptest.NewRecorder()); err != nil {
		t.Fatal(err)
	}
}

func (s failingSessionStore) Get(r *http.Request, name string) (*sessions.Session, error) {
	return sessions.NewSession(s, name), nil
}
func (s failingSessionStore) New(r *http.Request, name string) (*sessions.Session, error) {
	return sessions.NewSession(s, name), nil
}
func (s failingSessionStore) Save(r *http.Request, w http.ResponseWriter, sess *sessions.Session) error {
	return s.err
}

func TestSessionPersistenceErrorsAreReturned(t *testing.T) {
	want := errors.New("disk full")
	store := &Store{sessionsPath: t.TempDir(), UserStore: failingSessionStore{err: want}}
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	response := httptest.NewRecorder()
	ctx := NewContextWithStore(echo.New().NewContext(request, response), store)

	sess, err := store.UserStore.Get(request, "session")
	require.NoError(t, err)
	require.ErrorIs(t, ctx.SaveSession(sess), want)
	require.ErrorIs(t, ctx.UpdateSession(func(sess *sessions.Session) error {
		sess.Values["pending-access-token-value"] = "secret"
		return nil
	}), want)
}
