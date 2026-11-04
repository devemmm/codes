package context

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"sync"

	"github.com/gorilla/securecookie"
	"github.com/gorilla/sessions"
)

const sessionMaxAgeKey = "_codes-session-max-age"

const defaultSessionMaxAge = 30 * 24 * 60 * 60

var (
	ErrSessionConflict = errors.New("concurrent session update conflict")
	ErrSessionExpired  = errors.New("session expired or was deleted during request")
)

type sessionBaseline struct {
	values map[interface{}]interface{}
	maxAge int
	isNew  bool
}

type sessionBaselineKey struct {
	store *guardedSessionStore
	name  string
}

type requestSessionStateKey struct{}

type requestSessionState struct {
	mu        sync.Mutex
	baselines map[sessionBaselineKey]sessionBaseline
}

// guardedSessionStore serializes session saves across processes and applies
// request-local changes to the latest stored session under that lock.
type guardedSessionStore struct {
	sessionsPath string
	inner        sessions.Store
}

func (s *guardedSessionStore) Get(r *http.Request, name string) (*sessions.Session, error) {
	return sessions.GetRegistry(r).Get(s, name)
}

func (s *guardedSessionStore) New(r *http.Request, name string) (*sessions.Session, error) {
	inner, err := s.inner.New(r, name)
	if err != nil {
		return inner, err
	}
	values, err := cloneSessionValues(inner.Values)
	if err != nil {
		return nil, fmt.Errorf("snapshot session values: %w", err)
	}
	baselineValues, err := cloneSessionValues(values)
	if err != nil {
		return nil, fmt.Errorf("snapshot session baseline: %w", err)
	}
	wrapped := sessions.NewSession(s, name)
	wrapped.ID = inner.ID
	wrapped.Values = values
	wrapped.Options = inner.Options
	wrapped.IsNew = inner.IsNew
	if age, ok := values[sessionMaxAgeKey].(int); ok && age > 0 {
		wrapped.Options.MaxAge = age
	}
	state := sessionStateForRequest(r)
	state.mu.Lock()
	state.baselines[sessionBaselineKey{store: s, name: name}] = sessionBaseline{values: baselineValues, maxAge: wrapped.Options.MaxAge, isNew: wrapped.IsNew}
	state.mu.Unlock()
	return wrapped, nil
}

func (s *guardedSessionStore) Save(r *http.Request, w http.ResponseWriter, sess *sessions.Session) error {
	state := sessionStateForRequest(r)
	if sess.Options != nil && sess.Options.MaxAge <= 0 {
		err := s.saveWithLock(sess.ID, func() error { return s.inner.Save(r, w, sess) })
		if err == nil {
			state.mu.Lock()
			delete(state.baselines, sessionBaselineKey{store: s, name: sess.Name()})
			state.mu.Unlock()
		}
		return err
	}
	if sess.ID == "" {
		if sess.Options != nil && sess.Options.MaxAge != defaultSessionMaxAge {
			applySessionMaxAge(sess, nil, 0)
		}
		if err := s.inner.Save(r, w, sess); err != nil {
			return err
		}
		return state.updateBaseline(s, sess.Name(), sess)
	}

	baseline, ok := state.baseline(s, sess.Name())
	if !ok {
		return errors.New("session baseline is unavailable; refusing an unmerged save")
	}
	return s.saveWithLock(sess.ID, func() error {
		freshRequest := r.Clone(context.Background())
		latest, err := s.inner.New(freshRequest, sess.Name())
		if err != nil {
			return err
		}
		if !baseline.isNew && latest.IsNew {
			return ErrSessionExpired
		}
		candidate, err := cloneSessionValues(sess.Values)
		if err != nil {
			return fmt.Errorf("snapshot candidate session: %w", err)
		}
		if sess.Options != nil && sess.Options.MaxAge != baseline.maxAge {
			applySessionMaxAge(sess, candidate, baseline.maxAge)
			candidate, err = cloneSessionValues(sess.Values)
			if err != nil {
				return fmt.Errorf("snapshot candidate session: %w", err)
			}
		}
		latestValues, err := cloneSessionValues(latest.Values)
		if err != nil {
			return fmt.Errorf("snapshot latest session: %w", err)
		}
		if err := mergeSessionValues(baseline.values, candidate, latestValues); err != nil {
			return err
		}
		latest.Values = latestValues
		if age, ok := latestValues[sessionMaxAgeKey].(int); ok && age > 0 {
			latest.Options.MaxAge = age
		}
		if err := s.inner.Save(r, w, latest); err != nil {
			return err
		}
		copyValues, err := cloneSessionValues(latest.Values)
		if err != nil {
			return fmt.Errorf("snapshot saved session: %w", err)
		}
		state.setBaseline(s, sess.Name(), sessionBaseline{values: copyValues, maxAge: latest.Options.MaxAge, isNew: false})
		return nil
	})
}

func (s *guardedSessionStore) saveWithLock(id string, fn func() error) error {
	if id == "" {
		return fn()
	}
	return (&Store{sessionsPath: s.sessionsPath}).withSessionLock(id, fn)
}

func (s *requestSessionState) baseline(store *guardedSessionStore, name string) (sessionBaseline, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.baselines[sessionBaselineKey{store: store, name: name}]
	return b, ok
}

func (s *requestSessionState) setBaseline(store *guardedSessionStore, name string, b sessionBaseline) {
	s.mu.Lock()
	s.baselines[sessionBaselineKey{store: store, name: name}] = b
	s.mu.Unlock()
}

func (s *requestSessionState) updateBaseline(store *guardedSessionStore, name string, sess *sessions.Session) error {
	values, err := cloneSessionValues(sess.Values)
	if err != nil {
		return fmt.Errorf("snapshot saved session: %w", err)
	}
	s.setBaseline(store, name, sessionBaseline{values: values, maxAge: sess.Options.MaxAge, isNew: false})
	return nil
}

func sessionStateForRequest(r *http.Request) *requestSessionState {
	if state, ok := r.Context().Value(requestSessionStateKey{}).(*requestSessionState); ok {
		return state
	}
	state := &requestSessionState{baselines: make(map[sessionBaselineKey]sessionBaseline)}
	*r = *r.WithContext(context.WithValue(r.Context(), requestSessionStateKey{}, state))
	return state
}

func cloneSessionValues(values map[interface{}]interface{}) (map[interface{}]interface{}, error) {
	if values == nil {
		return make(map[interface{}]interface{}), nil
	}
	encoded, err := (securecookie.GobEncoder{}).Serialize(values)
	if err != nil {
		return nil, err
	}
	var clone map[interface{}]interface{}
	if err := (securecookie.GobEncoder{}).Deserialize(encoded, &clone); err != nil {
		return nil, err
	}
	if clone == nil {
		clone = make(map[interface{}]interface{})
	}
	return clone, nil
}

func mergeSessionValues(baseline, candidate, latest map[interface{}]interface{}) error {
	keys := make(map[interface{}]struct{}, len(baseline)+len(candidate))
	for key := range baseline {
		keys[key] = struct{}{}
	}
	for key := range candidate {
		keys[key] = struct{}{}
	}
	for key := range keys {
		baseValue, baseOK := baseline[key]
		candidateValue, candidateOK := candidate[key]
		if sameSessionEntry(baseValue, baseOK, candidateValue, candidateOK) {
			continue
		}
		latestValue, latestOK := latest[key]
		if !sameSessionEntry(baseValue, baseOK, latestValue, latestOK) &&
			!sameSessionEntry(candidateValue, candidateOK, latestValue, latestOK) {
			return fmt.Errorf("%w: key %v changed concurrently", ErrSessionConflict, key)
		}
		if candidateOK {
			latest[key] = candidateValue
		} else {
			delete(latest, key)
		}
	}
	return nil
}

func sameSessionEntry(a interface{}, aOK bool, b interface{}, bOK bool) bool {
	return aOK == bOK && (!aOK || reflect.DeepEqual(a, b))
}

func applySessionMaxAge(sess *sessions.Session, values map[interface{}]interface{}, baseline int) {
	if sess.Options == nil || sess.Options.MaxAge == baseline {
		return
	}
	if sess.Options.MaxAge == defaultSessionMaxAge {
		delete(sess.Values, sessionMaxAgeKey)
		if values != nil {
			delete(values, sessionMaxAgeKey)
		}
		return
	}
	sess.Values[sessionMaxAgeKey] = sess.Options.MaxAge
	if values != nil {
		values[sessionMaxAgeKey] = sess.Options.MaxAge
	}
}
