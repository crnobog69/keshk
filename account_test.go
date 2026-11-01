package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
)

func TestLegacyAvatarsDeletedAndProfileFlow(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")

	// Направи базу са старом base64 профилном у кључу "avatar" пре отварања Store-а.
	rawDB, err := sql.Open("sqlite", "file:"+filepath.ToSlash(dbPath))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rawDB.Exec(`CREATE TABLE settings (key TEXT PRIMARY KEY, value TEXT NOT NULL);
		INSERT INTO settings (key, value) VALUES ('avatar', 'data:image/png;base64,AAAA');`); err != nil {
		t.Fatal(err)
	}
	rawDB.Close()

	s, err := openStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if got := s.GetSetting("avatar"); got != "" {
		t.Fatalf("стара профилна слика није обрисана: %q", got)
	}

	// Додај Кешкић из септембра и провери да EnsureMemberSince узима најранији датум.
	if _, err := s.CreateKeshkic(Keshkic{
		Month: "2026-09", Title: "Мама", Amount: 5000, Currency: "RSD", Rate: 1, AmountRSD: 5000, IsDefault: true,
	}); err != nil {
		t.Fatal(err)
	}
	ms := s.EnsureMemberSince(time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC))
	if ms != "2026-09-01" {
		t.Fatalf("очекивано memberSince=2026-09-01, добијено %q", ms)
	}

	cfg := Config{
		Username:       "admin",
		Password:       "tajna123",
		Secret:         []byte("0123456789abcdef0123456789abcdef"),
		DefaultKeshkic: 5000,
		FallbackRates:  map[string]float64{"RSD": 1, "EUR": 117.2, "USD": 100},
	}
	h := &Handlers{cfg: cfg, store: s, rates: newRates(cfg.FallbackRates), auth: newAuth(cfg, ""), limiter: newLoginLimiter()}
	app := fiber.New()
	api := app.Group("/api", h.auth.Middleware)
	api.Post("/login", h.Login)
	api.Get("/me", h.Me)
	api.Put("/account", h.UpdateAccount)
	api.Get("/users/:handle", h.GetUser)

	// 1. Непријављен корисник НЕ МОЖЕ да види профил.
	req := httptest.NewRequest(http.MethodGet, "/api/users/@admin", nil)
	res, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("непријављен захтев очекивао 401, добио %d", res.StatusCode)
	}

	// 2. Пријава.
	loginReq := httptest.NewRequest(http.MethodPost, "/api/login", bytes.NewBufferString(`{"username":"admin","password":"tajna123"}`))
	loginReq.Header.Set("Content-Type", "application/json")
	loginRes, err := app.Test(loginReq)
	if err != nil || loginRes.StatusCode != http.StatusOK {
		t.Fatalf("пријава није успела: %v %d", err, loginRes.StatusCode)
	}
	var sessionCookie *http.Cookie
	for _, c := range loginRes.Cookies() {
		if c.Name == cookieName {
			sessionCookie = c
		}
	}
	if sessionCookie == nil {
		t.Fatal("нема колачића сесије")
	}

	// 3. base64 или javascript: линк за профилну се одбија.
	badReq := httptest.NewRequest(http.MethodPut, "/api/account", bytes.NewBufferString(`{"avatar":"data:image/png;base64,AAA"}`))
	badReq.Header.Set("Content-Type", "application/json")
	badReq.AddCookie(sessionCookie)
	badRes, _ := app.Test(badReq)
	if badRes.StatusCode != http.StatusBadRequest {
		t.Fatalf("очекивано 400 за base64 аватар, добијено %d", badRes.StatusCode)
	}

	// 4. Ажурирање профила преко линкова, описа и имена.
	updBody := `{"displayName":"Вук","bio":"Пратим своје Кешкиће.","avatar":"https://sen.crnbg.org/f/avatar1","banner":"https://sen.crnbg.org/f/banner1"}`
	updReq := httptest.NewRequest(http.MethodPut, "/api/account", bytes.NewBufferString(updBody))
	updReq.Header.Set("Content-Type", "application/json")
	updReq.AddCookie(sessionCookie)
	updRes, err := app.Test(updReq)
	if err != nil || updRes.StatusCode != http.StatusOK {
		t.Fatalf("ажурирање профила није успело: %v %d", err, updRes.StatusCode)
	}

	// 5. Преглед сопственог профила преко /api/users/@admin.
	profReq := httptest.NewRequest(http.MethodGet, "/api/users/@admin", nil)
	profReq.AddCookie(sessionCookie)
	profRes, err := app.Test(profReq)
	if err != nil || profRes.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/users/@admin није успео: %v %d", err, profRes.StatusCode)
	}
	var prof userProfile
	if err := json.NewDecoder(profRes.Body).Decode(&prof); err != nil {
		t.Fatal(err)
	}
	if prof.Username != "admin" || prof.DisplayName != "Вук" || prof.Bio != "Пратим своје Кешкиће." ||
		prof.Avatar != "https://sen.crnbg.org/f/avatar1" || prof.Banner != "https://sen.crnbg.org/f/banner1" ||
		prof.MemberSince != "2026-09-01" || !prof.IsSelf {
		t.Fatalf("неочекивани подаци профила: %+v", prof)
	}

	// 6. Непостојећи корисник враћа 404.
	nfReq := httptest.NewRequest(http.MethodGet, "/api/users/@neko", nil)
	nfReq.AddCookie(sessionCookie)
	nfRes, _ := app.Test(nfReq)
	if nfRes.StatusCode != http.StatusNotFound {
		t.Fatalf("очекивано 404 за непостојећег корисника, добијено %d", nfRes.StatusCode)
	}
}
