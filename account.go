package main

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gofiber/fiber/v3"
)

const (
	settingDisplayName = "display_name"
	settingAvatarURL   = "avatar_url"
	settingBannerURL   = "banner_url"
	settingBio         = "bio"
	settingMemberSince = "member_since"
	settingPassHash    = "password_hash"

	maxImageURLLen = 500
	maxBioRunes    = 240
	pbkdf2Iter     = 210000
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

// EnsureMemberSince обезбеђује трајно забележен датум учлањења (YYYY-MM-DD).
// За постојећу базу узима најранији датум из записа, а за празну данашњи дан.
func (s *Store) EnsureMemberSince(now time.Time) string {
	if cur := s.GetSetting(settingMemberSince); cur != "" {
		if _, err := time.Parse("2006-01-02", cur); err == nil {
			return cur
		}
	}
	var earliest string
	_ = s.db.QueryRow(`
		SELECT COALESCE(MIN(d), '') FROM (
			SELECT month || '-01' AS d FROM keshkici WHERE length(month) = 7
			UNION ALL SELECT substr(created_at, 1, 10) AS d FROM keshkici WHERE length(created_at) >= 10
			UNION ALL SELECT date AS d FROM transactions WHERE length(date) = 10
			UNION ALL SELECT substr(created_at, 1, 10) AS d FROM shopping_lists WHERE length(created_at) >= 10
		)`).Scan(&earliest)
	if _, err := time.Parse("2006-01-02", earliest); err != nil {
		earliest = now.Format("2006-01-02")
	}
	_ = s.SetSetting(settingMemberSince, earliest)
	return earliest
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
// HTTP: Налог и профили корисника (/u/@korisnik)
// ---------------------------------------------------------------------

type accountInfo struct {
	Username        string  `json:"username"`
	DisplayName     string  `json:"displayName"`
	Avatar          string  `json:"avatar"`
	Banner          string  `json:"banner"`
	Bio             string  `json:"bio"`
	MemberSince     string  `json:"memberSince"`
	DefaultKeshkic  float64 `json:"defaultKeshkic"`
	PasswordChanged bool    `json:"passwordChanged"`
}

type userProfile struct {
	Username    string `json:"username"`
	DisplayName string `json:"displayName"`
	Avatar      string `json:"avatar"`
	Banner      string `json:"banner"`
	Bio         string `json:"bio"`
	MemberSince string `json:"memberSince"`
	IsSelf      bool   `json:"isSelf"`
}

func (h *Handlers) account() accountInfo {
	return accountInfo{
		Username:        h.cfg.Username,
		DisplayName:     h.store.GetSetting(settingDisplayName),
		Avatar:          h.store.GetSetting(settingAvatarURL),
		Banner:          h.store.GetSetting(settingBannerURL),
		Bio:             h.store.GetSetting(settingBio),
		MemberSince:     h.store.EnsureMemberSince(time.Now()),
		DefaultKeshkic:  h.cfg.DefaultKeshkic,
		PasswordChanged: h.store.GetSetting(settingPassHash) != "",
	}
}

func (h *Handlers) Me(c fiber.Ctx) error {
	return c.JSON(h.account())
}

// GetUser: GET /api/users/:handle (захтева пријаву преко Auth middleware-а).
// Прихвата и "@admin" и "admin". Спремно за више налога касније.
func (h *Handlers) GetUser(c fiber.Ctx) error {
	raw, err := url.PathUnescape(c.Params("handle"))
	if err != nil {
		raw = c.Params("handle")
	}
	username := strings.TrimPrefix(strings.TrimSpace(raw), "@")
	if username == "" {
		return fail(c, fiber.StatusBadRequest, "Неисправно корисничко име")
	}
	if !strings.EqualFold(username, h.cfg.Username) {
		return fail(c, fiber.StatusNotFound, "Корисник није пронађен")
	}
	a := h.account()
	return c.JSON(userProfile{
		Username:    a.Username,
		DisplayName: a.DisplayName,
		Avatar:      a.Avatar,
		Banner:      a.Banner,
		Bio:         a.Bio,
		MemberSince: a.MemberSince,
		IsSelf:      true,
	})
}

func validImageURL(raw string) bool {
	if len(raw) > maxImageURLLen {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	scheme := strings.ToLower(u.Scheme)
	return (scheme == "http" || scheme == "https") && u.Host != ""
}

// UpdateAccount: PUT /api/account {"displayName"?, "avatar"?, "banner"?, "bio"?}
func (h *Handlers) UpdateAccount(c fiber.Ctx) error {
	var body struct {
		DisplayName *string `json:"displayName"`
		Avatar      *string `json:"avatar"`
		Banner      *string `json:"banner"`
		Bio         *string `json:"bio"`
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
	if body.Bio != nil {
		bio := strings.TrimSpace(strings.ReplaceAll(*body.Bio, "\r\n", "\n"))
		if utf8.RuneCountInString(bio) > maxBioRunes {
			return fail(c, fiber.StatusBadRequest, "Опис је предугачак (највише 240 знакова)")
		}
		if err := h.store.SetSetting(settingBio, bio); err != nil {
			return internal(c, err)
		}
	}
	if body.Avatar != nil {
		a := strings.TrimSpace(*body.Avatar)
		if a != "" && !validImageURL(a) {
			return fail(c, fiber.StatusBadRequest, "Профилна слика мора бити важећи http:// или https:// линк")
		}
		if err := h.store.SetSetting(settingAvatarURL, a); err != nil {
			return internal(c, err)
		}
	}
	if body.Banner != nil {
		b := strings.TrimSpace(*body.Banner)
		if b != "" && !validImageURL(b) {
			return fail(c, fiber.StatusBadRequest, "Позадина мора бити важећи http:// или https:// линк")
		}
		if err := h.store.SetSetting(settingBannerURL, b); err != nil {
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
