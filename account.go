package main

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gofiber/fiber/v3"
)

const (
	settingDisplayName = "display_name"
	settingAvatar      = "avatar"
	settingPassHash    = "password_hash"

	maxAvatarLen = 600 * 1024 // data URL (base64) — клијент шаље 256x256 JPEG
	pbkdf2Iter   = 210000
)

// ---------------------------------------------------------------------
// Подешавања (key/value)
// ---------------------------------------------------------------------

func (s *Store) GetSetting(key string) string {
	var v string
	if err := s.db.QueryRow(`SELECT value FROM settings WHERE key = ?`, key).Scan(&v); err != nil {
		return ""
	}
	return v
}

func (s *Store) SetSetting(key, value string) error {
	if value == "" {
		_, err := s.db.Exec(`DELETE FROM settings WHERE key = ?`, key)
		return err
	}
	_, err := s.db.Exec(`
		INSERT INTO settings (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}

// ---------------------------------------------------------------------
// Лозинка (PBKDF2-SHA256)
// ---------------------------------------------------------------------

func hashPassword(pw string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key, err := pbkdf2.Key(sha256.New, pw, salt, pbkdf2Iter, 32)
	if err != nil {
		return "", err
	}
	return "pbkdf2-sha256$" + strconv.Itoa(pbkdf2Iter) + "$" + hex.EncodeToString(salt) + "$" + hex.EncodeToString(key), nil
}

func verifyPassword(encoded, pw string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 4 || parts[0] != "pbkdf2-sha256" {
		return false
	}
	iter, err := strconv.Atoi(parts[1])
	if err != nil || iter < 1 {
		return false
	}
	salt, err1 := hex.DecodeString(parts[2])
	want, err2 := hex.DecodeString(parts[3])
	if err1 != nil || err2 != nil || len(want) == 0 {
		return false
	}
	got, err := pbkdf2.Key(sha256.New, pw, salt, iter, len(want))
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare(got, want) == 1
}

// ---------------------------------------------------------------------
// HTTP: Налог
// ---------------------------------------------------------------------

type accountInfo struct {
	Username        string  `json:"username"`
	DisplayName     string  `json:"displayName"`
	Avatar          string  `json:"avatar"`
	DefaultKeshkic  float64 `json:"defaultKeshkic"`
	PasswordChanged bool    `json:"passwordChanged"`
}

func (h *Handlers) account() accountInfo {
	return accountInfo{
		Username:        h.cfg.Username,
		DisplayName:     h.store.GetSetting(settingDisplayName),
		Avatar:          h.store.GetSetting(settingAvatar),
		DefaultKeshkic:  h.cfg.DefaultKeshkic,
		PasswordChanged: h.store.GetSetting(settingPassHash) != "",
	}
}

func (h *Handlers) Me(c fiber.Ctx) error {
	return c.JSON(h.account())
}

func validAvatar(a string) bool {
	for _, p := range []string{"data:image/jpeg;base64,", "data:image/png;base64,", "data:image/webp;base64,"} {
		if strings.HasPrefix(a, p) {
			return true
		}
	}
	return false
}

// UpdateAccount: PUT /api/account {"displayName"?: "...", "avatar"?: "data:image/...;base64,..." | ""}
func (h *Handlers) UpdateAccount(c fiber.Ctx) error {
	var body struct {
		DisplayName *string `json:"displayName"`
		Avatar      *string `json:"avatar"`
	}
	if err := c.Bind().JSON(&body); err != nil {
		return fail(c, fiber.StatusBadRequest, "Неисправан захтев")
	}
	if body.DisplayName != nil {
		name := strings.TrimSpace(*body.DisplayName)
		if utf8.RuneCountInString(name) > 40 {
			return fail(c, fiber.StatusBadRequest, "Име је предугачко (највише 40 знакова)")
		}
		if err := h.store.SetSetting(settingDisplayName, name); err != nil {
			return internal(c, err)
		}
	}
	if body.Avatar != nil {
		a := strings.TrimSpace(*body.Avatar)
		if a != "" {
			if !validAvatar(a) {
				return fail(c, fiber.StatusBadRequest, "Неподржан формат слике (JPEG, PNG или WebP)")
			}
			if len(a) > maxAvatarLen {
				return fail(c, fiber.StatusBadRequest, "Слика је превелика")
			}
		}
		if err := h.store.SetSetting(settingAvatar, a); err != nil {
			return internal(c, err)
		}
	}
	return c.JSON(h.account())
}

// ChangePassword: PUT /api/account/password {"current": "...", "next": "..."}
func (h *Handlers) ChangePassword(c fiber.Ctx) error {
	var body struct {
		Current string `json:"current"`
		Next    string `json:"next"`
	}
	if err := c.Bind().JSON(&body); err != nil {
		return fail(c, fiber.StatusBadRequest, "Неисправан захтев")
	}
	if !h.auth.checkCredentials(h.cfg.Username, body.Current) {
		time.Sleep(400 * time.Millisecond)
		return fail(c, fiber.StatusBadRequest, "Тренутна лозинка није тачна")
	}
	if utf8.RuneCountInString(body.Next) < 6 {
		return fail(c, fiber.StatusBadRequest, "Нова лозинка мора имати бар 6 знакова")
	}
	if utf8.RuneCountInString(body.Next) > 128 {
		return fail(c, fiber.StatusBadRequest, "Нова лозинка је предугачка")
	}
	hash, err := hashPassword(body.Next)
	if err != nil {
		return internal(c, err)
	}
	if err := h.store.SetSetting(settingPassHash, hash); err != nil {
		return internal(c, err)
	}
	// Нова лозинка поништава све остале сесије; тренутној издајемо нову.
	h.auth.setPasswordHash(hash)
	h.auth.setSession(c)
	return c.JSON(fiber.Map{"ok": true})
}
