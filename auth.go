package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gofiber/fiber/v3"
)

const (
	cookieName  = "keshk_session"
	sessionTTL  = 30 * 24 * time.Hour
	loginPath   = "/api/login"
	logoutPath  = "/api/logout"
)

// Auth implements a tiny stateless session: cookie = "<expiry>.<hmac>".
// If a password hash is stored in the DB (changed via Налог), it overrides .env.
type Auth struct {
	user   string
	pass   string
	secret []byte
	secure bool

	mu       sync.RWMutex
	passHash string
}

func newAuth(cfg Config, passHash string) *Auth {
	return &Auth{user: cfg.Username, pass: cfg.Password, secret: cfg.Secret, secure: cfg.CookieSecure, passHash: passHash}
}

func (a *Auth) setPasswordHash(h string) {
	a.mu.Lock()
	a.passHash = h
	a.mu.Unlock()
}

func (a *Auth) currentHash() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.passHash
}

func (a *Auth) sign(exp int64) string {
	mac := hmac.New(sha256.New, a.secret)
	msg := strconv.FormatInt(exp, 10) + "|" + a.user
	if ph := a.currentHash(); ph != "" {
		msg += "|" + ph // промена лозинке поништава старе сесије
	}
	mac.Write([]byte(msg))
	return hex.EncodeToString(mac.Sum(nil))
}

func (a *Auth) issue() (string, time.Time) {
	expires := time.Now().Add(sessionTTL)
	exp := expires.Unix()
	return strconv.FormatInt(exp, 10) + "." + a.sign(exp), expires
}

func (a *Auth) verify(token string) bool {
	expStr, sig, ok := strings.Cut(token, ".")
	if !ok {
		return false
	}
	exp, err := strconv.ParseInt(expStr, 10, 64)
	if err != nil || time.Now().Unix() > exp {
		return false
	}
	return hmac.Equal([]byte(sig), []byte(a.sign(exp)))
}

func (a *Auth) checkCredentials(user, pass string) bool {
	hash := func(s string) []byte { h := sha256.Sum256([]byte(s)); return h[:] }
	userOK := subtle.ConstantTimeCompare(hash(user), hash(a.user)) == 1
	var passOK bool
	if ph := a.currentHash(); ph != "" {
		passOK = verifyPassword(ph, pass)
	} else {
		passOK = subtle.ConstantTimeCompare(hash(pass), hash(a.pass)) == 1
	}
	return userOK && passOK
}

func (a *Auth) setSession(c fiber.Ctx) {
	token, expires := a.issue()
	c.Cookie(&fiber.Cookie{
		Name:     cookieName,
		Value:    token,
		Path:     "/",
		Expires:  expires,
		HTTPOnly: true,
		Secure:   a.secure,
		SameSite: "Lax",
	})
}

func (a *Auth) clearSession(c fiber.Ctx) {
	c.Cookie(&fiber.Cookie{
		Name:     cookieName,
		Value:    "",
		Path:     "/",
		Expires:  time.Unix(0, 0),
		MaxAge:   -1,
		HTTPOnly: true,
		Secure:   a.secure,
		SameSite: "Lax",
	})
}

// Middleware protects every /api route except login/logout.
func (a *Auth) Middleware(c fiber.Ctx) error {
	p := c.Path()
	if p == loginPath || p == logoutPath {
		return c.Next()
	}
	if !a.verify(c.Cookies(cookieName)) {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Нисте пријављени"})
	}
	return c.Next()
}
