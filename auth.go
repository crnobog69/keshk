package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
)

const (
	cookieName          = "keshk_session"
	sessionTTL          = 30 * 24 * time.Hour
	userContextKey      = "keshk_user"
	loginPath           = "/api/login"
	logoutPath          = "/api/logout"
	verifyCodePath      = "/api/register/verify-code"
	checkUsernamePath   = "/api/register/check-username"
	registerPath        = "/api/register"
)

// Auth издаје и проверава потписане сесионе колачиће за више корисника:
// cookie = "<userID>.<expiry>.<hmac(userID|expiry|passwordHash)>".
// Промена лозинке или брисање корисника аутоматски поништава старе сесије.
type Auth struct {
	store  *Store
	secret []byte
	secure bool
}

func newAuth(cfg Config, store *Store) *Auth {
	return &Auth{store: store, secret: cfg.Secret, secure: cfg.CookieSecure}
}

func (a *Auth) sign(userID, exp int64, passHash string) string {
	mac := hmac.New(sha256.New, a.secret)
	msg := strconv.FormatInt(userID, 10) + "|" + strconv.FormatInt(exp, 10) + "|" + passHash
	mac.Write([]byte(msg))
	return hex.EncodeToString(mac.Sum(nil))
}

func (a *Auth) issue(u User) (string, time.Time) {
	expires := time.Now().Add(sessionTTL)
	exp := expires.Unix()
	uid := strconv.FormatInt(u.ID, 10)
	return uid + "." + strconv.FormatInt(exp, 10) + "." + a.sign(u.ID, exp, u.PasswordHash), expires
}

func (a *Auth) verify(token string) (User, bool) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return User{}, false
	}
	uid, err1 := strconv.ParseInt(parts[0], 10, 64)
	exp, err2 := strconv.ParseInt(parts[1], 10, 64)
	if err1 != nil || err2 != nil || uid <= 0 || time.Now().Unix() > exp {
		return User{}, false
	}
	u, err := a.store.GetUserByID(uid)
	if err != nil {
		return User{}, false
	}
	expected := a.sign(u.ID, exp, u.PasswordHash)
	if !hmac.Equal([]byte(parts[2]), []byte(expected)) {
		return User{}, false
	}
	return u, true
}

func (a *Auth) setSession(c fiber.Ctx, u User) {
	token, expires := a.issue(u)
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

func isPublicAPIPath(p string) bool {
	return p == loginPath ||
		p == logoutPath ||
		p == verifyCodePath ||
		p == checkUsernamePath ||
		p == registerPath
}

// Middleware protects every /api route except login, logout and invite registration.
func (a *Auth) Middleware(c fiber.Ctx) error {
	if isPublicAPIPath(c.Path()) {
		return c.Next()
	}
	u, ok := a.verify(c.Cookies(cookieName))
	if !ok {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "Нисте пријављени"})
	}
	c.Locals(userContextKey, u)
	return c.Next()
}

// AdminMiddleware дозвољава приступ само првом (админ) налогу.
func (a *Auth) AdminMiddleware(c fiber.Ctx) error {
	u := currentUser(c)
	if !u.IsAdmin {
		return fail(c, fiber.StatusForbidden, "Приступ је дозвољен само администратору")
	}
	return c.Next()
}

func currentUser(c fiber.Ctx) User {
	if u, ok := c.Locals(userContextKey).(User); ok {
		return u
	}
	return User{}
}
