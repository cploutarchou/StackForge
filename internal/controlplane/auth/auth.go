package auth

import (
	"crypto/sha256"
	"crypto/subtle"
	"net/http"
	"strings"
)

type Authenticator struct {
	hashes [][]byte
}

func New(keys []string) *Authenticator {
	a := &Authenticator{}
	for _, key := range keys {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		sum := sha256.Sum256([]byte(key))
		a.hashes = append(a.hashes, sum[:])
	}
	return a
}

func (a *Authenticator) Enabled() bool {
	return len(a.hashes) > 0
}

func (a *Authenticator) Authorize(header string) bool {
	if !strings.HasPrefix(header, "Bearer ") {
		return false
	}
	key := strings.TrimPrefix(header, "Bearer ")
	sum := sha256.Sum256([]byte(key))
	for _, h := range a.hashes {
		if subtle.ConstantTimeCompare(sum[:], h) == 1 {
			return true
		}
	}
	return false
}

func (a *Authenticator) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !a.Enabled() {
			http.Error(w, "admin API keys are required", http.StatusForbidden)
			return
		}
		if !a.Authorize(r.Header.Get("Authorization")) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}
