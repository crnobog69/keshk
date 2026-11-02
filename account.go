package main

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"errors"
	"log"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gofiber/fiber/v3"
	"rsc.io/qr"
)

const (
	maxImageURLLen = 500
	maxBioRunes    = 240
	pbkdf2Iter     = 210000
	adminPageSize  = 15

	inviteAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
)

var (
	errInvalidInvite      = errors.New("invalid invite code")
	errUsernameTaken      = errors.New("username taken")
	errCannotDelAdm       = errors.New("cannot delete admin")
	errDeletionCodeExists = errors.New("deletion code already exists")
	usernameRe            = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,22}[a-zA-Z0-9_]$`)
	dummyPassHash         string
)

func init() {
	h, err := hashPassword("keshk-dummy-timing-guard")
	if err == nil {
		dummyPassHash = h
	}
}

// User представља регистрован кориснички налог. Само први налог има IsAdmin = true.
type User struct {
	ID                  int64  `json:"id"`
	Username            string `json:"username"`
	PasswordHash        string `json:"-"`
	DisplayName         string `json:"displayName"`
	AvatarURL           string `json:"avatar"`
	BannerURL           string `json:"banner"`
	Bio                 string `json:"bio"`
	MemberSince         string `json:"memberSince"`
	IsAdmin             bool   `json:"isAdmin"`
	DeletionCode        string `json:"deletionCode,omitempty"`
	DeletionRequestedAt string `json:"deletionRequestedAt,omitempty"`
	CreatedAt           string `json:"createdAt"`
}

// InviteCode је једнократни позивни код за регистрацију новог налога који генерише админ.
type InviteCode struct {
	ID             int64  `json:"id"`
	Code           string `json:"code"`
	CreatedBy      int64  `json:"createdBy"`
	UsedBy         int64  `json:"usedBy"`
	UsedByUsername string `json:"usedByUsername,omitempty"`
	UsedAt         string `json:"usedAt,omitempty"`
	CreatedAt      string `json:"createdAt"`
}

// EnsureMemberSince одређује датум учлањења (YYYY-MM-DD) за први налог.
// За постојећу базу узима најранији датум из записа, а за празну данашњи дан.
func (s *Store) EnsureMemberSince(now time.Time) string {
	var earliest string
	_ = s.db.QueryRow(`
		SELECT COALESCE(MIN(d), '') FROM (
			SELECT month || '-01' AS d FROM keshkici WHERE length(month) = 7
			UNION ALL SELECT substr(created_at, 1, 10) AS d FROM keshkici WHERE length(created_at) >= 10
			UNION ALL SELECT date AS d FROM transactions WHERE length(date) = 10
			UNION ALL SELECT substr(created_at, 1, 10) AS d FROM shopping_lists WHERE length(created_at) >= 10
		) AS dates`).Scan(&earliest)
	if _, err := time.Parse("2006-01-02", earliest); err != nil {
		earliest = now.Format("2006-01-02")
	}
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
// Корисници и позивни кодови (Store методе)
// ---------------------------------------------------------------------

const userColumns = `id, username, password_hash, display_name, avatar_url, banner_url, bio, member_since, is_admin, deletion_code, deletion_requested_at, created_at`

func scanUser(sc interface{ Scan(...any) error }) (User, error) {
	var u User
	var adm int
	err := sc.Scan(
		&u.ID, &u.Username, &u.PasswordHash, &u.DisplayName,
		&u.AvatarURL, &u.BannerURL, &u.Bio, &u.MemberSince,
		&adm, &u.DeletionCode, &u.DeletionRequestedAt, &u.CreatedAt,
	)
	u.IsAdmin = adm == 1
	return u, err
}

func (s *Store) GetUserByID(id int64) (User, error) {
	u, err := scanUser(s.db.QueryRow(`SELECT `+userColumns+` FROM users WHERE id = $1`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return u, errNotFound
	}
	return u, err
}

func (s *Store) GetUserByUsername(username string) (User, error) {
	username = strings.TrimPrefix(strings.TrimSpace(username), "@")
	if username == "" {
		return User{}, errNotFound
	}
	u, err := scanUser(s.db.QueryRow(`SELECT `+userColumns+` FROM users WHERE LOWER(username) = LOWER($1)`, username))
	if errors.Is(err, sql.ErrNoRows) {
		return u, errNotFound
	}
	return u, err
}

// EnsureAdminUser осигурава да први (администраторски) налог постоји у табели users.
func (s *Store) EnsureAdminUser(cfg Config, now time.Time) (User, error) {
	u, err := scanUser(s.db.QueryRow(`SELECT ` + userColumns + ` FROM users WHERE is_admin = 1 ORDER BY id ASC LIMIT 1`))
	if err == nil {
		if cfg.ResetPassword {
			h, err := hashPassword(cfg.Password)
			if err != nil {
				return u, err
			}
			if _, err := s.db.Exec(`UPDATE users SET password_hash = $1 WHERE id = $2`, h, u.ID); err != nil {
				return u, err
			}
			u.PasswordHash = h
			log.Println("[INFO] Лозинка администратора враћена на вредност из .env (KESHK_RESET_PASSWORD)")
		}
		if u.MemberSince == "" {
			ms := s.EnsureMemberSince(now)
			_, _ = s.db.Exec(`UPDATE users SET member_since = $1 WHERE id = $2`, ms, u.ID)
			u.MemberSince = ms
		}
		return u, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return User{}, err
	}

	passHash, err := hashPassword(cfg.Password)
	if err != nil {
		return User{}, err
	}
	memberSince := s.EnsureMemberSince(now)

	var id int64
	err = s.db.QueryRow(`
		INSERT INTO users (username, password_hash, display_name, avatar_url, banner_url, bio, member_since, is_admin)
		VALUES ($1, $2, '', '', '', '', $3, 1)
		RETURNING id`,
		cfg.Username, passHash, memberSince,
	).Scan(&id)
	if err != nil {
		return User{}, err
	}

	// Повежи евентуалне постојеће записе из једнокорисничке шеме са овим админ налогом.
	if id != 1 {
		_, _ = s.db.Exec(`UPDATE keshkici SET user_id = $1 WHERE user_id = 1`, id)
		_, _ = s.db.Exec(`UPDATE transactions SET user_id = $1 WHERE user_id = 1`, id)
		_, _ = s.db.Exec(`UPDATE shopping_lists SET user_id = $1 WHERE user_id = 1`, id)
	}
	return s.GetUserByID(id)
}

func (s *Store) AuthenticateUser(username, password string) (User, bool) {
	u, err := s.GetUserByUsername(username)
	if err != nil {
		if dummyPassHash != "" {
			_ = verifyPassword(dummyPassHash, password)
		}
		return User{}, false
	}
	if !verifyPassword(u.PasswordHash, password) {
		return User{}, false
	}
	return u, true
}

type AdminStats struct {
	TotalUsers    int `json:"totalUsers"`
	ActiveInvites int `json:"activeInvites"`
	UsedInvites   int `json:"usedInvites"`
}

type PageMeta struct {
	Page       int    `json:"page"`
	PageSize   int    `json:"pageSize"`
	Total      int    `json:"total"`
	TotalPages int    `json:"totalPages"`
	Query      string `json:"query,omitempty"`
}

func escapeLike(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(s)
}

func (s *Store) GetAdminStats() (AdminStats, error) {
	var st AdminStats
	err := s.db.QueryRow(`
		SELECT
			(SELECT COUNT(*) FROM users),
			(SELECT COUNT(*) FROM invite_codes WHERE used_by IS NULL AND used_at = ''),
			(SELECT COUNT(*) FROM invite_codes WHERE used_by IS NOT NULL OR used_at <> '')
	`).Scan(&st.TotalUsers, &st.ActiveInvites, &st.UsedInvites)
	return st, err
}

func (s *Store) ListPendingDeletions() ([]User, error) {
	rows, err := s.db.Query(`
		SELECT ` + userColumns + `
		FROM users
		WHERE is_admin = 0 AND deletion_code <> ''
		ORDER BY deletion_requested_at DESC, id DESC
		LIMIT 50`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []User{}
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func (s *Store) ListUsersPage(rawQuery string, page, pageSize int) ([]User, PageMeta, error) {
	if pageSize <= 0 {
		pageSize = adminPageSize
	}
	q := strings.TrimSpace(rawQuery)
	if utf8.RuneCountInString(q) > 60 {
		runes := []rune(q)
		q = string(runes[:60])
	}

	where := ""
	var args []any
	if q != "" {
		clean := strings.TrimPrefix(q, "@")
		pat := "%" + escapeLike(strings.ToLower(clean)) + "%"
		args = append(args, pat)
		idCand := strings.TrimPrefix(q, "#")
		if idVal, err := strconv.ParseInt(idCand, 10, 64); err == nil && idVal > 0 {
			args = append(args, idVal)
			where = ` WHERE (LOWER(username) LIKE $1 ESCAPE '\' OR LOWER(display_name) LIKE $1 ESCAPE '\' OR LOWER(deletion_code) LIKE $1 ESCAPE '\' OR id = $2)`
		} else {
			where = ` WHERE (LOWER(username) LIKE $1 ESCAPE '\' OR LOWER(display_name) LIKE $1 ESCAPE '\' OR LOWER(deletion_code) LIKE $1 ESCAPE '\')`
		}
	}

	var total int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM users`+where, args...).Scan(&total); err != nil {
		return nil, PageMeta{}, err
	}

	totalPages := max(1, (total+pageSize-1)/pageSize)
	if page < 1 {
		page = 1
	} else if page > totalPages {
		page = totalPages
	}
	offset := (page - 1) * pageSize

	limIdx := len(args) + 1
	offIdx := len(args) + 2
	qArgs := append(append([]any{}, args...), pageSize, offset)

	rows, err := s.db.Query(
		`SELECT `+userColumns+` FROM users`+where+
			` ORDER BY is_admin DESC, id ASC LIMIT $`+strconv.Itoa(limIdx)+` OFFSET $`+strconv.Itoa(offIdx),
		qArgs...,
	)
	if err != nil {
		return nil, PageMeta{}, err
	}
	defer rows.Close()

	out := []User{}
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, PageMeta{}, err
		}
		out = append(out, u)
	}
	if err := rows.Err(); err != nil {
		return nil, PageMeta{}, err
	}
	return out, PageMeta{
		Page:       page,
		PageSize:   pageSize,
		Total:      total,
		TotalPages: totalPages,
		Query:      q,
	}, nil
}

func (s *Store) UpdateUserProfile(userID int64, displayName, bio, avatarURL, bannerURL *string) (User, error) {
	u, err := s.GetUserByID(userID)
	if err != nil {
		return User{}, err
	}
	if displayName != nil {
		u.DisplayName = *displayName
	}
	if bio != nil {
		u.Bio = *bio
	}
	if avatarURL != nil {
		u.AvatarURL = *avatarURL
	}
	if bannerURL != nil {
		u.BannerURL = *bannerURL
	}
	_, err = s.db.Exec(`
		UPDATE users
		SET display_name = $1, bio = $2, avatar_url = $3, banner_url = $4
		WHERE id = $5`,
		u.DisplayName, u.Bio, u.AvatarURL, u.BannerURL, userID)
	if err != nil {
		return User{}, err
	}
	return s.GetUserByID(userID)
}

func (s *Store) UpdateUserPassword(userID int64, passHash string) (User, error) {
	res, err := s.db.Exec(`UPDATE users SET password_hash = $1 WHERE id = $2`, passHash, userID)
	if err != nil {
		return User{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return User{}, errNotFound
	}
	return s.GetUserByID(userID)
}

func (s *Store) DeleteUser(targetID int64) error {
	u, err := s.GetUserByID(targetID)
	if err != nil {
		return err
	}
	if u.IsAdmin {
		return errCannotDelAdm
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for _, q := range []string{
		`DELETE FROM shopping_items WHERE list_id IN (SELECT id FROM shopping_lists WHERE user_id = $1)`,
		`DELETE FROM shopping_lists WHERE user_id = $1`,
		`DELETE FROM transactions WHERE user_id = $1`,
		`DELETE FROM keshkici WHERE user_id = $1`,
		`DELETE FROM users WHERE id = $1 AND is_admin = 0`,
	} {
		if _, err := tx.Exec(q, targetID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ClearUserData брише изабране податке корисничког налога (листе, трошкове и/или Кешкиће)
// без брисања самог корисничког налога.
func (s *Store) ClearUserData(userID int64, clearKeshkici, clearTxs, clearLists bool) error {
	if clearKeshkici {
		clearTxs = true
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var stmts []string
	if clearLists {
		stmts = append(stmts,
			`DELETE FROM shopping_items WHERE list_id IN (SELECT id FROM shopping_lists WHERE user_id = $1)`,
			`DELETE FROM shopping_lists WHERE user_id = $1`,
		)
	}
	if clearKeshkici {
		if !clearLists {
			stmts = append(stmts, `UPDATE shopping_lists SET keshkic_id = NULL, tx_id = NULL WHERE user_id = $1`)
		}
		stmts = append(stmts,
			`DELETE FROM transactions WHERE user_id = $1`,
			`DELETE FROM keshkici WHERE user_id = $1`,
		)
	} else if clearTxs {
		if !clearLists {
			stmts = append(stmts, `UPDATE shopping_lists SET tx_id = NULL WHERE user_id = $1`)
		}
		stmts = append(stmts, `DELETE FROM transactions WHERE user_id = $1`)
	}

	for _, q := range stmts {
		if _, err := tx.Exec(q, userID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// GenerateDeletionCode генерише потврдни код за захтев за брисање налога.
// Код се може генерисати САМО ЈЕДНОМ док га администратор не потврди (обрише налог) или поништи.
func (s *Store) GenerateDeletionCode(userID int64, now time.Time) (User, error) {
	u, err := s.GetUserByID(userID)
	if err != nil {
		return User{}, err
	}
	if u.IsAdmin {
		return User{}, errCannotDelAdm
	}
	if u.DeletionCode != "" {
		return u, errDeletionCodeExists
	}

	code, err := generateInviteCodeString()
	if err != nil {
		return User{}, err
	}
	reqAt := now.UTC().Format("2006-01-02T15:04:05Z")

	res, err := s.db.Exec(`
		UPDATE users
		SET deletion_code = $1, deletion_requested_at = $2
		WHERE id = $3 AND is_admin = 0 AND deletion_code = ''`,
		code, reqAt, userID,
	)
	if err != nil {
		return User{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		cur, err := s.GetUserByID(userID)
		if err != nil {
			return User{}, err
		}
		if cur.DeletionCode != "" {
			return cur, errDeletionCodeExists
		}
		return User{}, errCannotDelAdm
	}
	return s.GetUserByID(userID)
}

// ClearUserDeletionCode поништава захтев/код за брисање налога (само за администратора).
func (s *Store) ClearUserDeletionCode(targetID int64) error {
	res, err := s.db.Exec(`UPDATE users SET deletion_code = '', deletion_requested_at = '' WHERE id = $1`, targetID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errNotFound
	}
	return nil
}

func generateInviteCodeString() (string, error) {
	raw := make([]byte, 12)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	var b strings.Builder
	b.Grow(14)
	for i, v := range raw {
		if i == 4 || i == 8 {
			b.WriteByte('-')
		}
		b.WriteByte(inviteAlphabet[int(v)&31])
	}
	return b.String(), nil
}

func normalizeInviteCode(raw string) string {
	var clean strings.Builder
	for _, r := range strings.ToUpper(strings.TrimSpace(raw)) {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			clean.WriteRune(r)
		}
	}
	s := clean.String()
	if len(s) == 12 {
		return s[0:4] + "-" + s[4:8] + "-" + s[8:12]
	}
	return s
}

func (s *Store) CreateInviteCode(adminID int64) (InviteCode, error) {
	for range 5 {
		code, err := generateInviteCodeString()
		if err != nil {
			return InviteCode{}, err
		}
		var ic InviteCode
		err = s.db.QueryRow(`
			INSERT INTO invite_codes (code, created_by)
			VALUES ($1, $2)
			RETURNING id, code, created_by, created_at`,
			code, adminID,
		).Scan(&ic.ID, &ic.Code, &ic.CreatedBy, &ic.CreatedAt)
		if err == nil {
			return ic, nil
		}
	}
	return InviteCode{}, errors.New("неуспешно генерисање кода")
}

func (s *Store) ListInviteCodesPage(page, pageSize, knownTotal int) ([]InviteCode, PageMeta, error) {
	if pageSize <= 0 {
		pageSize = adminPageSize
	}
	total := knownTotal
	if total < 0 {
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM invite_codes`).Scan(&total); err != nil {
			return nil, PageMeta{}, err
		}
	}
	totalPages := max(1, (total+pageSize-1)/pageSize)
	if page < 1 {
		page = 1
	} else if page > totalPages {
		page = totalPages
	}
	offset := (page - 1) * pageSize

	rows, err := s.db.Query(`
		SELECT ic.id, ic.code, ic.created_by, COALESCE(ic.used_by, 0),
		       CASE
		           WHEN ic.used_by IS NOT NULL THEN COALESCE(u.username, 'обрисан налог')
		           ELSE ''
		       END,
		       ic.used_at, ic.created_at
		FROM invite_codes ic
		LEFT JOIN users u ON u.id = ic.used_by
		ORDER BY (ic.used_at <> '' OR ic.used_by IS NOT NULL) ASC, ic.id DESC
		LIMIT $1 OFFSET $2`, pageSize, offset)
	if err != nil {
		return nil, PageMeta{}, err
	}
	defer rows.Close()
	out := []InviteCode{}
	for rows.Next() {
		var ic InviteCode
		if err := rows.Scan(&ic.ID, &ic.Code, &ic.CreatedBy, &ic.UsedBy, &ic.UsedByUsername, &ic.UsedAt, &ic.CreatedAt); err != nil {
			return nil, PageMeta{}, err
		}
		out = append(out, ic)
	}
	if err := rows.Err(); err != nil {
		return nil, PageMeta{}, err
	}
	return out, PageMeta{
		Page:       page,
		PageSize:   pageSize,
		Total:      total,
		TotalPages: totalPages,
	}, nil
}

func (s *Store) DeleteInviteCode(id int64) error {
	res, err := s.db.Exec(`DELETE FROM invite_codes WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errNotFound
	}
	return nil
}

func (s *Store) VerifyInviteCode(raw string) (string, bool) {
	code := normalizeInviteCode(raw)
	if len(code) != 14 {
		return "", false
	}
	var dbCode string
	err := s.db.QueryRow(`SELECT code FROM invite_codes WHERE code = $1 AND used_by IS NULL AND used_at = ''`, code).Scan(&dbCode)
	if err != nil {
		return "", false
	}
	return dbCode, true
}

func cleanAndValidateUsername(raw string) (string, error) {
	u := strings.TrimPrefix(strings.TrimSpace(raw), "@")
	if len(u) < 2 || len(u) > 24 || !usernameRe.MatchString(u) || strings.Contains(u, "..") {
		return "", errors.New("Корисничко име мора имати 2–24 знака (латиница a-z, бројеви, тачка, цртица или доња црта)")
	}
	return u, nil
}

type registerParams struct {
	Code        string
	Username    string
	Password    string
	DisplayName string
	Avatar      string
	Banner      string
	Bio         string
}

func (s *Store) RegisterWithInvite(p registerParams, now time.Time) (User, error) {
	code := normalizeInviteCode(p.Code)
	if len(code) != 14 {
		return User{}, errInvalidInvite
	}
	passHash, err := hashPassword(p.Password)
	if err != nil {
		return User{}, err
	}

	tx, err := s.db.Begin()
	if err != nil {
		return User{}, err
	}
	defer tx.Rollback()

	var inviteID int64
	err = tx.QueryRow(`
		SELECT id FROM invite_codes
		WHERE code = $1 AND used_by IS NULL AND used_at = ''
		FOR UPDATE`, code).Scan(&inviteID)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, errInvalidInvite
	}
	if err != nil {
		return User{}, err
	}

	var taken int
	if err := tx.QueryRow(`SELECT 1 FROM users WHERE LOWER(username) = LOWER($1)`, p.Username).Scan(&taken); err == nil {
		return User{}, errUsernameTaken
	} else if !errors.Is(err, sql.ErrNoRows) {
		return User{}, err
	}

	memberSince := now.Format("2006-01-02")
	var userID int64
	err = tx.QueryRow(`
		INSERT INTO users (username, password_hash, display_name, avatar_url, banner_url, bio, member_since, is_admin)
		VALUES ($1, $2, $3, $4, $5, $6, $7, 0)
		RETURNING id`,
		p.Username, passHash, p.DisplayName, p.Avatar, p.Banner, p.Bio, memberSince,
	).Scan(&userID)
	if err != nil {
		return User{}, errUsernameTaken
	}

	usedAt := now.UTC().Format("2006-01-02T15:04:05Z")
	if _, err := tx.Exec(`UPDATE invite_codes SET used_by = $1, used_at = $2 WHERE id = $3`, userID, usedAt, inviteID); err != nil {
		return User{}, err
	}

	if err := tx.Commit(); err != nil {
		return User{}, err
	}
	return s.GetUserByID(userID)
}

// ---------------------------------------------------------------------
// HTTP: Налог, профили корисника (/u/@korisnik), регистрација и Админ
// ---------------------------------------------------------------------

type accountInfo struct {
	ID                  int64   `json:"id"`
	Username            string  `json:"username"`
	DisplayName         string  `json:"displayName"`
	Avatar              string  `json:"avatar"`
	Banner              string  `json:"banner"`
	Bio                 string  `json:"bio"`
	MemberSince         string  `json:"memberSince"`
	DefaultKeshkic      float64 `json:"defaultKeshkic"`
	IsAdmin             bool    `json:"isAdmin"`
	DeletionCode        string  `json:"deletionCode,omitempty"`
	DeletionRequestedAt string  `json:"deletionRequestedAt,omitempty"`
}

type userProfile struct {
	Username    string `json:"username"`
	DisplayName string `json:"displayName"`
	Avatar      string `json:"avatar"`
	Banner      string `json:"banner"`
	Bio         string `json:"bio"`
	MemberSince string `json:"memberSince"`
	IsSelf      bool   `json:"isSelf"`
	IsAdmin     bool   `json:"isAdmin"`
}

func (h *Handlers) accountFor(u User) accountInfo {
	ms := u.MemberSince
	if ms == "" {
		ms = time.Now().Format("2006-01-02")
	}
	return accountInfo{
		ID:                  u.ID,
		Username:            u.Username,
		DisplayName:         u.DisplayName,
		Avatar:              u.AvatarURL,
		Banner:              u.BannerURL,
		Bio:                 u.Bio,
		MemberSince:         ms,
		DefaultKeshkic:      h.cfg.DefaultKeshkic,
		IsAdmin:             u.IsAdmin,
		DeletionCode:        u.DeletionCode,
		DeletionRequestedAt: u.DeletionRequestedAt,
	}
}

func (h *Handlers) Me(c fiber.Ctx) error {
	return c.JSON(h.accountFor(currentUser(c)))
}

// GetUser: GET /api/users/:handle (захтева пријаву преко Auth middleware-а).
// Прихвата и "@korisnik" и "korisnik" за било који регистрован налог.
func (h *Handlers) GetUser(c fiber.Ctx) error {
	raw, err := url.PathUnescape(c.Params("handle"))
	if err != nil {
		raw = c.Params("handle")
	}
	username := strings.TrimPrefix(strings.TrimSpace(raw), "@")
	if username == "" {
		return fail(c, fiber.StatusBadRequest, "Неисправно корисничко име")
	}
	u, err := h.store.GetUserByUsername(username)
	if errors.Is(err, errNotFound) {
		return fail(c, fiber.StatusNotFound, "Корисник није пронађен")
	} else if err != nil {
		return internal(c, err)
	}
	me := currentUser(c)
	return c.JSON(userProfile{
		Username:    u.Username,
		DisplayName: u.DisplayName,
		Avatar:      u.AvatarURL,
		Banner:      u.BannerURL,
		Bio:         u.Bio,
		MemberSince: u.MemberSince,
		IsSelf:      u.ID == me.ID,
		IsAdmin:     u.IsAdmin,
	})
}

func renderQRCodeSVG(code *qr.Code) []byte {
	const margin = 4
	total := code.Size + margin*2
	var b strings.Builder
	b.Grow(4096)
	b.WriteString(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 `)
	b.WriteString(strconv.Itoa(total))
	b.WriteByte(' ')
	b.WriteString(strconv.Itoa(total))
	b.WriteString(`" shape-rendering="crispEdges"><rect width="100%" height="100%" fill="#ffffff"/><path fill="#09090b" d="`)
	for y := 0; y < code.Size; y++ {
		x := 0
		for x < code.Size {
			if !code.Black(x, y) {
				x++
				continue
			}
			run := 1
			for x+run < code.Size && code.Black(x+run, y) {
				run++
			}
			b.WriteByte('M')
			b.WriteString(strconv.Itoa(x + margin))
			b.WriteByte(' ')
			b.WriteString(strconv.Itoa(y + margin))
			b.WriteByte('h')
			b.WriteString(strconv.Itoa(run))
			b.WriteString(`v1h-`)
			b.WriteString(strconv.Itoa(run))
			b.WriteByte('z')
			x += run
		}
	}
	b.WriteString(`"/></svg>`)
	return []byte(b.String())
}

// GetUserQR: GET /api/users/:handle/qr[?origin=https://...&format=png]
// Генерише чист векторски SVG (или PNG) QR код са линком ка налогу /u/@korisnik.
func (h *Handlers) GetUserQR(c fiber.Ctx) error {
	raw, err := url.PathUnescape(c.Params("handle"))
	if err != nil {
		raw = c.Params("handle")
	}
	username := strings.TrimPrefix(strings.TrimSpace(raw), "@")
	if username == "" {
		return fail(c, fiber.StatusBadRequest, "Неисправно корисничко име")
	}
	u, err := h.store.GetUserByUsername(username)
	if errors.Is(err, errNotFound) {
		return fail(c, fiber.StatusNotFound, "Корисник није пронађен")
	} else if err != nil {
		return internal(c, err)
	}

	baseOrigin := ""
	if rawOrigin := strings.TrimSpace(c.Query("origin")); rawOrigin != "" && len(rawOrigin) <= 200 {
		if parsed, err := url.Parse(rawOrigin); err == nil {
			scheme := strings.ToLower(parsed.Scheme)
			if (scheme == "http" || scheme == "https") && parsed.Host != "" {
				baseOrigin = scheme + "://" + parsed.Host
			}
		}
	}
	if baseOrigin == "" {
		proto := strings.TrimSpace(c.Get("X-Forwarded-Proto"))
		if proto != "http" && proto != "https" {
			proto = c.Scheme()
		}
		host := c.Host()
		if host == "" {
			host = "localhost"
		}
		baseOrigin = proto + "://" + host
	}

	profileURL := baseOrigin + "/u/@" + url.PathEscape(u.Username)
	code, err := qr.Encode(profileURL, qr.M)
	if err != nil {
		return internal(c, err)
	}

	if strings.EqualFold(strings.TrimSpace(c.Query("format")), "png") {
		code.Scale = 10
		c.Set("Content-Type", "image/png")
		c.Set("Content-Disposition", `attachment; filename="keshk-@`+u.Username+`-qr.png"`)
		return c.Send(code.PNG())
	}

	c.Set("Content-Type", "image/svg+xml; charset=utf-8")
	return c.Send(renderQRCodeSVG(code))
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

	var namePtr, bioPtr, avatarPtr, bannerPtr *string
	if body.DisplayName != nil {
		name := strings.TrimSpace(*body.DisplayName)
		if utf8.RuneCountInString(name) > 40 {
			return fail(c, fiber.StatusBadRequest, "Име је предугачко (највише 40 знакова)")
		}
		namePtr = &name
	}
	if body.Bio != nil {
		bio := strings.TrimSpace(strings.ReplaceAll(*body.Bio, "\r\n", "\n"))
		if utf8.RuneCountInString(bio) > maxBioRunes {
			return fail(c, fiber.StatusBadRequest, "Опис је предугачак (највише 240 знакова)")
		}
		bioPtr = &bio
	}
	if body.Avatar != nil {
		a := strings.TrimSpace(*body.Avatar)
		if a != "" && !validImageURL(a) {
			return fail(c, fiber.StatusBadRequest, "Профилна слика мора бити важећи http:// или https:// линк")
		}
		avatarPtr = &a
	}
	if body.Banner != nil {
		b := strings.TrimSpace(*body.Banner)
		if b != "" && !validImageURL(b) {
			return fail(c, fiber.StatusBadRequest, "Позадина мора бити важећи http:// или https:// линк")
		}
		bannerPtr = &b
	}

	updated, err := h.store.UpdateUserProfile(currentUser(c).ID, namePtr, bioPtr, avatarPtr, bannerPtr)
	if err != nil {
		return internal(c, err)
	}
	return c.JSON(h.accountFor(updated))
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
	me := currentUser(c)
	if !verifyPassword(me.PasswordHash, body.Current) {
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
	updated, err := h.store.UpdateUserPassword(me.ID, hash)
	if err != nil {
		return internal(c, err)
	}
	// Нова лозинка поништава све остале сесије овог корисника; тренутној издајемо нову.
	h.auth.setSession(c, updated)
	return c.JSON(fiber.Map{"ok": true})
}

// ClearAccountData: POST /api/account/clear-data {"keshkici": bool, "transactions": bool, "lists": bool}
func (h *Handlers) ClearAccountData(c fiber.Ctx) error {
	var body struct {
		Keshkici     bool `json:"keshkici"`
		Transactions bool `json:"transactions"`
		Lists        bool `json:"lists"`
	}
	if err := c.Bind().JSON(&body); err != nil {
		return fail(c, fiber.StatusBadRequest, "Неисправан захтев")
	}
	if !body.Keshkici && !body.Transactions && !body.Lists {
		return fail(c, fiber.StatusBadRequest, "Изабери бар једну врсту података за брисање")
	}
	if err := h.store.ClearUserData(currentUser(c).ID, body.Keshkici, body.Transactions, body.Lists); err != nil {
		return internal(c, err)
	}
	return c.JSON(fiber.Map{"ok": true})
}

// CreateDeletionCode: POST /api/account/deletion-code
// Генерише једнократни потврдни код за захтев за брисање налога (само једном док га админ не потврди).
func (h *Handlers) CreateDeletionCode(c fiber.Ctx) error {
	u, err := h.store.GenerateDeletionCode(currentUser(c).ID, time.Now())
	if errors.Is(err, errCannotDelAdm) {
		return fail(c, fiber.StatusBadRequest, "Главни администраторски налог се не може обрисати")
	}
	if errors.Is(err, errDeletionCodeExists) {
		return fail(c, fiber.StatusConflict, "Код за брисање налога је већ генерисан и важи док га администратор не потврди")
	}
	if err != nil {
		return internal(c, err)
	}
	return c.Status(fiber.StatusCreated).JSON(h.accountFor(u))
}

// ---------------------------------------------------------------------
// HTTP: Позивни код и Onboarding регистрација (уз rate limit)
// ---------------------------------------------------------------------

// VerifyInviteCode: POST /api/register/verify-code {"code": "..."}
func (h *Handlers) VerifyInviteCode(c fiber.Ctx) error {
	ip := clientIP(c)
	if blocked, wait := h.inviteLimiter.blocked(ip); blocked {
		return rejectRateLimited(c, wait)
	}
	var body struct {
		Code string `json:"code"`
	}
	if err := c.Bind().JSON(&body); err != nil {
		return fail(c, fiber.StatusBadRequest, "Неисправан захтев")
	}
	if strings.TrimSpace(body.Code) == "" {
		return fail(c, fiber.StatusBadRequest, "Унеси код за регистрацију")
	}
	code, ok := h.store.VerifyInviteCode(body.Code)
	if !ok {
		h.inviteLimiter.fail(ip)
		log.Printf("[WARN] неуспешан покушај кода за регистрацију са %s", ip)
		time.Sleep(350 * time.Millisecond)
		return fail(c, fiber.StatusBadRequest, "Код за регистрацију није важећи или је већ искоришћен")
	}
	return c.JSON(fiber.Map{"ok": true, "code": code})
}

// CheckUsername: POST /api/register/check-username {"code": "...", "username": "..."}
func (h *Handlers) CheckUsername(c fiber.Ctx) error {
	ip := clientIP(c)
	if blocked, wait := h.inviteLimiter.blocked(ip); blocked {
		return rejectRateLimited(c, wait)
	}
	var body struct {
		Code     string `json:"code"`
		Username string `json:"username"`
	}
	if err := c.Bind().JSON(&body); err != nil {
		return fail(c, fiber.StatusBadRequest, "Неисправан захтев")
	}
	if _, ok := h.store.VerifyInviteCode(body.Code); !ok {
		h.inviteLimiter.fail(ip)
		log.Printf("[WARN] провера корисничког имена без важећег кода са %s", ip)
		time.Sleep(350 * time.Millisecond)
		return fail(c, fiber.StatusBadRequest, "Код за регистрацију није важећи или је већ искоришћен")
	}
	username, err := cleanAndValidateUsername(body.Username)
	if err != nil {
		return fail(c, fiber.StatusBadRequest, err.Error())
	}
	if _, err := h.store.GetUserByUsername(username); err == nil {
		return fail(c, fiber.StatusConflict, "Корисничко име је већ заузето")
	} else if !errors.Is(err, errNotFound) {
		return internal(c, err)
	}
	return c.JSON(fiber.Map{"ok": true, "username": username})
}

// Register: POST /api/register
func (h *Handlers) Register(c fiber.Ctx) error {
	ip := clientIP(c)
	if blocked, wait := h.inviteLimiter.blocked(ip); blocked {
		return rejectRateLimited(c, wait)
	}
	var body struct {
		Code        string `json:"code"`
		Username    string `json:"username"`
		Password    string `json:"password"`
		DisplayName string `json:"displayName"`
		Avatar      string `json:"avatar"`
		Banner      string `json:"banner"`
		Bio         string `json:"bio"`
	}
	if err := c.Bind().JSON(&body); err != nil {
		return fail(c, fiber.StatusBadRequest, "Неисправан захтев")
	}
	if _, ok := h.store.VerifyInviteCode(body.Code); !ok {
		h.inviteLimiter.fail(ip)
		log.Printf("[WARN] неуспешна регистрација (неисправан код) са %s", ip)
		time.Sleep(350 * time.Millisecond)
		return fail(c, fiber.StatusBadRequest, "Код за регистрацију није важећи или је већ искоришћен")
	}

	username, err := cleanAndValidateUsername(body.Username)
	if err != nil {
		return fail(c, fiber.StatusBadRequest, err.Error())
	}
	if utf8.RuneCountInString(body.Password) < 6 {
		return fail(c, fiber.StatusBadRequest, "Лозинка мора имати бар 6 знакова")
	}
	if utf8.RuneCountInString(body.Password) > 128 {
		return fail(c, fiber.StatusBadRequest, "Лозинка је предугачка")
	}
	displayName := strings.TrimSpace(body.DisplayName)
	if utf8.RuneCountInString(displayName) > 40 {
		return fail(c, fiber.StatusBadRequest, "Име за приказ је предугачко (највише 40 знакова)")
	}
	bio := strings.TrimSpace(strings.ReplaceAll(body.Bio, "\r\n", "\n"))
	if utf8.RuneCountInString(bio) > maxBioRunes {
		return fail(c, fiber.StatusBadRequest, "Опис је предугачак (највише 240 знакова)")
	}
	avatar := strings.TrimSpace(body.Avatar)
	if avatar != "" && !validImageURL(avatar) {
		return fail(c, fiber.StatusBadRequest, "Профилна слика мора бити важећи http:// или https:// линк")
	}
	banner := strings.TrimSpace(body.Banner)
	if banner != "" && !validImageURL(banner) {
		return fail(c, fiber.StatusBadRequest, "Позадина мора бити важећи http:// или https:// линк")
	}

	user, err := h.store.RegisterWithInvite(registerParams{
		Code:        body.Code,
		Username:    username,
		Password:    body.Password,
		DisplayName: displayName,
		Avatar:      avatar,
		Banner:      banner,
		Bio:         bio,
	}, time.Now())
	if errors.Is(err, errInvalidInvite) {
		h.inviteLimiter.fail(ip)
		time.Sleep(350 * time.Millisecond)
		return fail(c, fiber.StatusBadRequest, "Код за регистрацију није важећи или је већ искоришћен")
	}
	if errors.Is(err, errUsernameTaken) {
		return fail(c, fiber.StatusConflict, "Корисничко име је већ заузето")
	}
	if err != nil {
		return internal(c, err)
	}

	h.inviteLimiter.success(ip)
	h.auth.setSession(c, user)
	return c.Status(fiber.StatusCreated).JSON(h.accountFor(user))
}

// ---------------------------------------------------------------------
// HTTP: Админ панел (/api/admin/* — само први налог)
// ---------------------------------------------------------------------

func parsePositiveInt(raw string, def int) int {
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || n < 1 {
		return def
	}
	return n
}

func (h *Handlers) AdminOverview(c fiber.Ctx) error {
	q := c.Query("q")
	page := parsePositiveInt(c.Query("page"), 1)
	invitesPage := parsePositiveInt(c.Query("invitesPage"), 1)

	stats, err := h.store.GetAdminStats()
	if err != nil {
		return internal(c, err)
	}
	pending, err := h.store.ListPendingDeletions()
	if err != nil {
		return internal(c, err)
	}
	users, usersMeta, err := h.store.ListUsersPage(q, page, adminPageSize)
	if err != nil {
		return internal(c, err)
	}
	invites, invitesMeta, err := h.store.ListInviteCodesPage(invitesPage, adminPageSize, stats.ActiveInvites+stats.UsedInvites)
	if err != nil {
		return internal(c, err)
	}
	return c.JSON(fiber.Map{
		"stats":            stats,
		"pendingDeletions": pending,
		"users":            users,
		"usersMeta":        usersMeta,
		"invites":          invites,
		"invitesMeta":      invitesMeta,
	})
}

func (h *Handlers) AdminCreateInvite(c fiber.Ctx) error {
	ic, err := h.store.CreateInviteCode(currentUser(c).ID)
	if err != nil {
		return internal(c, err)
	}
	return c.Status(fiber.StatusCreated).JSON(ic)
}

func (h *Handlers) AdminDeleteInvite(c fiber.Ctx) error {
	id, ok := paramID(c, "id")
	if !ok {
		return fail(c, fiber.StatusBadRequest, "Неисправан ID")
	}
	if err := h.store.DeleteInviteCode(id); errors.Is(err, errNotFound) {
		return fail(c, fiber.StatusNotFound, "Код није пронађен")
	} else if err != nil {
		return internal(c, err)
	}
	return c.SendStatus(fiber.StatusNoContent)
}

func (h *Handlers) AdminClearDeletionCode(c fiber.Ctx) error {
	id, ok := paramID(c, "id")
	if !ok {
		return fail(c, fiber.StatusBadRequest, "Неисправан ID")
	}
	if err := h.store.ClearUserDeletionCode(id); errors.Is(err, errNotFound) {
		return fail(c, fiber.StatusNotFound, "Корисник није пронађен")
	} else if err != nil {
		return internal(c, err)
	}
	return c.SendStatus(fiber.StatusNoContent)
}

func (h *Handlers) AdminDeleteUser(c fiber.Ctx) error {
	id, ok := paramID(c, "id")
	if !ok {
		return fail(c, fiber.StatusBadRequest, "Неисправан ID")
	}
	if id == currentUser(c).ID {
		return fail(c, fiber.StatusBadRequest, "Администраторски налог се не може обрисати")
	}
	err := h.store.DeleteUser(id)
	if errors.Is(err, errNotFound) {
		return fail(c, fiber.StatusNotFound, "Корисник није пронађен")
	}
	if errors.Is(err, errCannotDelAdm) {
		return fail(c, fiber.StatusBadRequest, "Администраторски налог се не може обрисати")
	}
	if err != nil {
		return internal(c, err)
	}
	return c.SendStatus(fiber.StatusNoContent)
}
