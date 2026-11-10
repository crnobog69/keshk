package main

import (
	"bytes"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/joho/godotenv"
)

// newTestSchemaURL креира изоловану привремену Postgres шему у Neon бази за потребе теста
// и аутоматски је брише (CASCADE) по завршетку теста, тако да public шема остаје нетакнута.
func newTestSchemaURL(t *testing.T) string {
	t.Helper()
	_ = godotenv.Load()

	baseURL := strings.TrimSpace(os.Getenv("DATABASE_URL_UNPOOLED"))
	if baseURL == "" {
		baseURL = strings.TrimSpace(os.Getenv("DATABASE_URL"))
	}
	if baseURL == "" {
		t.Fatal("DATABASE_URL_UNPOOLED / DATABASE_URL није постављен")
	}

	buf := make([]byte, 6)
	if _, err := rand.Read(buf); err != nil {
		t.Fatal(err)
	}
	schema := "keshk_test_" + hex.EncodeToString(buf)

	adminDB, err := openPG(baseURL, 2, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adminDB.Exec(`CREATE SCHEMA ` + schema); err != nil {
		_ = adminDB.Close()
		t.Fatalf("креирање тест шеме %s: %v", schema, err)
	}
	_ = adminDB.Close()

	t.Cleanup(func() {
		cleanDB, err := openPG(baseURL, 2, 1)
		if err != nil {
			return
		}
		defer cleanDB.Close()
		_, _ = cleanDB.Exec(`DROP SCHEMA IF EXISTS ` + schema + ` CASCADE`)
	})

	u, err := url.Parse(baseURL)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	return u.String()
}

func openTestStore(t *testing.T) *Store {
	t.Helper()
	schemaURL := newTestSchemaURL(t)
	s, err := openStore(schemaURL, schemaURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func buildTestApp(t *testing.T, s *Store) (*fiber.App, Config, User) {
	t.Helper()
	cfg := Config{
		Username:       "admin",
		Password:       "tajna123",
		Secret:         []byte("0123456789abcdef0123456789abcdef"),
		DefaultKeshkic: 5000,
		FallbackRates:  map[string]float64{"RSD": 1, "EUR": 117.2, "USD": 100},
	}
	adm, err := s.EnsureAdminUser(cfg, time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	h := &Handlers{
		cfg:           cfg,
		store:         s,
		rates:         newRates(cfg.FallbackRates),
		auth:          newAuth(cfg, s),
		limiter:       newLoginLimiter(),
		inviteLimiter: newInviteLimiter(),
	}
	app := fiber.New()
	api := app.Group("/api", h.auth.Middleware)
	api.Post("/login", h.Login)
	api.Post("/logout", h.Logout)
	api.Post("/register/verify-code", h.VerifyInviteCode)
	api.Post("/register/check-username", h.CheckUsername)
	api.Post("/register", h.Register)
	api.Get("/me", h.Me)
	api.Get("/months/:month", h.Month)
	api.Post("/keshkici", h.CreateKeshkic)
	api.Delete("/keshkici/:id", h.DeleteKeshkic)
	api.Post("/transactions", h.CreateTx)
	api.Post("/deposits", h.CreateDeposit)
	api.Put("/deposits/:id", h.UpdateDeposit)
	api.Delete("/deposits/:id", h.DeleteDeposit)
	api.Put("/account", h.UpdateAccount)
	api.Put("/account/password", h.ChangePassword)
	api.Post("/account/clear-data", h.ClearAccountData)
	api.Post("/account/deletion-code", h.CreateDeletionCode)
	api.Get("/users/:handle/qr", h.GetUserQR)
	api.Get("/users/:handle", h.GetUser)
	api.Get("/lists", h.ListLists)
	api.Post("/lists", h.CreateList)
	api.Get("/export", h.ExportData)
	api.Post("/import", h.ImportData)

	adminAPI := api.Group("/admin", h.auth.AdminMiddleware)
	adminAPI.Get("/overview", h.AdminOverview)
	adminAPI.Post("/invites", h.AdminCreateInvite)
	adminAPI.Delete("/invites/:id", h.AdminDeleteInvite)
	adminAPI.Delete("/users/:id/deletion-code", h.AdminClearDeletionCode)
	adminAPI.Delete("/users/:id", h.AdminDeleteUser)

	return app, cfg, adm
}

func sessionCookieFrom(res *http.Response) *http.Cookie {
	for _, c := range res.Cookies() {
		if c.Name == cookieName {
			return c
		}
	}
	return nil
}

func TestNeonPooledConnection(t *testing.T) {
	_ = godotenv.Load()
	dbURL := strings.TrimSpace(os.Getenv("DATABASE_URL"))
	directURL := strings.TrimSpace(os.Getenv("DATABASE_URL_UNPOOLED"))
	if dbURL == "" {
		t.Fatal("DATABASE_URL није постављен")
	}

	s, err := openStore(dbURL, directURL)
	if err != nil {
		t.Fatalf("openStore преко pooled DATABASE_URL није успео: %v", err)
	}
	defer s.Close()

	// Више узастопних упита преко PgBouncer transaction pooler-а доказује
	// да нема сукоба са prepared statements кешом.
	for range 5 {
		if _, err := s.MonthTotals(1, "2026-11"); err != nil {
			t.Fatalf("MonthTotals на pooled конекцији: %v", err)
		}
		if _, err := s.ListKeshkici(1, "2026-11"); err != nil {
			t.Fatalf("ListKeshkici на pooled конекцији: %v", err)
		}
		if _, err := s.ListShoppingLists(1); err != nil {
			t.Fatalf("ListShoppingLists на pooled конекцији: %v", err)
		}
	}
}

func TestLegacyAvatarsDeletedAndProfileFlow(t *testing.T) {
	schemaURL := newTestSchemaURL(t)

	// Направи табелу са старом base64 профилном у кључу "avatar" пре отварања Store-а.
	rawDB, err := openPG(schemaURL, 2, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rawDB.Exec(`CREATE TABLE settings (key TEXT PRIMARY KEY, value TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err := rawDB.Exec(`INSERT INTO settings (key, value) VALUES ('avatar', 'data:image/png;base64,AAAA')`); err != nil {
		t.Fatal(err)
	}
	_ = rawDB.Close()

	s, err := openStore(schemaURL, schemaURL)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	var reg sql.NullString
	if err := s.db.QueryRow(`SELECT to_regclass('settings')`).Scan(&reg); err != nil {
		t.Fatal(err)
	}
	if reg.Valid {
		t.Fatalf("застарела табела settings мора бити обрисана, а постоји: %q", reg.String)
	}

	// Додај Кешкић из септембра и провери да EnsureAdminUser преузима најранији датум.
	if _, err := s.CreateKeshkic(1, Keshkic{
		Month: "2026-09", Title: "Мама", Amount: 5000, Currency: "RSD", Rate: 1, AmountRSD: 5000, IsDefault: true,
	}); err != nil {
		t.Fatal(err)
	}

	app, _, adm := buildTestApp(t, s)
	if !adm.IsAdmin || adm.MemberSince != "2026-09-01" {
		t.Fatalf("очекиван админ са memberSince=2026-09-01, добијено %+v", adm)
	}

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
	sessionCookie := sessionCookieFrom(loginRes)
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
		prof.MemberSince != "2026-09-01" || !prof.IsSelf || !prof.IsAdmin {
		t.Fatalf("неочекивани подаци профила: %+v", prof)
	}

	// 6. QR код за @admin (SVG и PNG) и 404 за непостојећег корисника.
	qrSvgReq := httptest.NewRequest(http.MethodGet, "/api/users/@admin/qr?origin=https://keshk.crnbg.org", nil)
	qrSvgReq.AddCookie(sessionCookie)
	qrSvgRes, err := app.Test(qrSvgReq)
	if err != nil || qrSvgRes.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/users/@admin/qr није успео: %v %d", err, qrSvgRes.StatusCode)
	}
	svgBytes, _ := io.ReadAll(qrSvgRes.Body)
	if !strings.HasPrefix(string(svgBytes), "<svg ") {
		t.Fatalf("очекиван SVG QR код, добијено: %q", string(svgBytes))
	}

	qrPngReq := httptest.NewRequest(http.MethodGet, "/api/users/@admin/qr?origin=https://keshk.crnbg.org&format=png", nil)
	qrPngReq.AddCookie(sessionCookie)
	qrPngRes, err := app.Test(qrPngReq)
	if err != nil || qrPngRes.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/users/@admin/qr?format=png није успео: %v %d", err, qrPngRes.StatusCode)
	}
	pngBytes, _ := io.ReadAll(qrPngRes.Body)
	if len(pngBytes) < 8 || !bytes.HasPrefix(pngBytes, []byte("\x89PNG\r\n\x1a\n")) {
		t.Fatalf("очекиван валидан PNG QR код, дужина=%d", len(pngBytes))
	}

	nfReq := httptest.NewRequest(http.MethodGet, "/api/users/@neko", nil)
	nfReq.AddCookie(sessionCookie)
	nfRes, _ := app.Test(nfReq)
	if nfRes.StatusCode != http.StatusNotFound {
		t.Fatalf("очекивано 404 за непостојећег корисника, добијено %d", nfRes.StatusCode)
	}
}

func TestMultiAccountOnboardingAdminAndIsolation(t *testing.T) {
	s := openTestStore(t)
	app, _, _ := buildTestApp(t, s)

	// 1. Пријава првог налога (Админ).
	admLogin := httptest.NewRequest(http.MethodPost, "/api/login", bytes.NewBufferString(`{"username":"admin","password":"tajna123"}`))
	admLogin.Header.Set("Content-Type", "application/json")
	admLoginRes, err := app.Test(admLogin)
	if err != nil || admLoginRes.StatusCode != http.StatusOK {
		t.Fatalf("админ пријава није успела: %v %d", err, admLoginRes.StatusCode)
	}
	admCookie := sessionCookieFrom(admLoginRes)

	// 2. Админ генерише позивни код у Админ панелу.
	invReq := httptest.NewRequest(http.MethodPost, "/api/admin/invites", nil)
	invReq.AddCookie(admCookie)
	invRes, err := app.Test(invReq)
	if err != nil || invRes.StatusCode != http.StatusCreated {
		t.Fatalf("генерисање позивног кода није успело: %v %d", err, invRes.StatusCode)
	}
	var ic InviteCode
	if err := json.NewDecoder(invRes.Body).Decode(&ic); err != nil {
		t.Fatal(err)
	}
	if len(ic.Code) != 14 {
		t.Fatalf("неочекиван формат кода: %q", ic.Code)
	}

	// 3. Позвани корисник проверава код (чак и малим словима и без цртица).
	compactCode := strings.ToLower(strings.ReplaceAll(ic.Code, "-", ""))
	verReq := httptest.NewRequest(http.MethodPost, "/api/register/verify-code", bytes.NewBufferString(`{"code":"`+compactCode+`"}`))
	verReq.Header.Set("Content-Type", "application/json")
	verRes, err := app.Test(verReq)
	if err != nil || verRes.StatusCode != http.StatusOK {
		t.Fatalf("провера важећег кода није успела: %v %d", err, verRes.StatusCode)
	}

	// 4. Провера заузетог и слободног корисничког имена у Onboarding кораку 1.
	chkTaken := httptest.NewRequest(http.MethodPost, "/api/register/check-username", bytes.NewBufferString(`{"code":"`+ic.Code+`","username":"ADMIN"}`))
	chkTaken.Header.Set("Content-Type", "application/json")
	if r, _ := app.Test(chkTaken); r.StatusCode != http.StatusConflict {
		t.Fatalf("очекивано 409 за заузето име, добијено %d", r.StatusCode)
	}
	chkFree := httptest.NewRequest(http.MethodPost, "/api/register/check-username", bytes.NewBufferString(`{"code":"`+ic.Code+`","username":"@marko"}`))
	chkFree.Header.Set("Content-Type", "application/json")
	if r, _ := app.Test(chkFree); r.StatusCode != http.StatusOK {
		t.Fatalf("очекивано 200 за слободно име, добијено %d", r.StatusCode)
	}

	// 5. Завршетак Onboarding-а (лозинка, корисничко име, профилна, позадина).
	regBody := `{
		"code": "` + ic.Code + `",
		"username": "marko",
		"password": "markovaLozinka99",
		"displayName": "Марко",
		"avatar": "https://sen.crnbg.org/f/marko-av",
		"banner": "https://sen.crnbg.org/f/marko-bg",
		"bio": "Нови члан"
	}`
	regReq := httptest.NewRequest(http.MethodPost, "/api/register", bytes.NewBufferString(regBody))
	regReq.Header.Set("Content-Type", "application/json")
	regRes, err := app.Test(regReq)
	if err != nil || regRes.StatusCode != http.StatusCreated {
		t.Fatalf("регистрација није успела: %v %d", err, regRes.StatusCode)
	}
	var markoMe accountInfo
	if err := json.NewDecoder(regRes.Body).Decode(&markoMe); err != nil {
		t.Fatal(err)
	}
	if markoMe.IsAdmin {
		t.Fatal("други регистровани налог НЕ СМЕ бити админ")
	}
	if markoMe.Username != "marko" || markoMe.Avatar != "https://sen.crnbg.org/f/marko-av" || markoMe.Banner != "https://sen.crnbg.org/f/marko-bg" {
		t.Fatalf("неочекивани подаци новог налога: %+v", markoMe)
	}
	markoCookie := sessionCookieFrom(regRes)
	if markoCookie == nil {
		t.Fatal("регистрација није издала колачић сесије")
	}

	// 6. Исти позивни код се НЕ МОЖЕ поново искористити.
	reuseReq := httptest.NewRequest(http.MethodPost, "/api/register/verify-code", bytes.NewBufferString(`{"code":"`+ic.Code+`"}`))
	reuseReq.Header.Set("Content-Type", "application/json")
	if r, _ := app.Test(reuseReq); r.StatusCode != http.StatusBadRequest {
		t.Fatalf("очекивано 400 за већ искоришћен код, добијено %d", r.StatusCode)
	}

	// 7. Обичан корисник НЕМА приступ Админ панелу (403 Forbidden).
	for _, ep := range []struct{ method, path string }{
		{http.MethodGet, "/api/admin/overview"},
		{http.MethodPost, "/api/admin/invites"},
	} {
		r := httptest.NewRequest(ep.method, ep.path, nil)
		r.AddCookie(markoCookie)
		res, _ := app.Test(r)
		if res.StatusCode != http.StatusForbidden {
			t.Fatalf("не-админ %s %s очекивао 403, добио %d", ep.method, ep.path, res.StatusCode)
		}
	}

	// 8. Потпуна изолација података између Админа и Марка.
	admKReq := httptest.NewRequest(http.MethodPost, "/api/keshkici", bytes.NewBufferString(`{"month":"2026-11","title":"Админ Буџет","amount":50000,"currency":"RSD","isDefault":true}`))
	admKReq.Header.Set("Content-Type", "application/json")
	admKReq.AddCookie(admCookie)
	admKRes, _ := app.Test(admKReq)
	var admK Keshkic
	_ = json.NewDecoder(admKRes.Body).Decode(&admK)

	markoMonthReq := httptest.NewRequest(http.MethodGet, "/api/months/2026-11", nil)
	markoMonthReq.AddCookie(markoCookie)
	markoMonthRes, _ := app.Test(markoMonthReq)
	var markoMonth struct {
		Summary MonthSummary `json:"summary"`
	}
	_ = json.NewDecoder(markoMonthRes.Body).Decode(&markoMonth)
	if len(markoMonth.Summary.Keshkici) != 0 || markoMonth.Summary.TotalKeshkic != 0 {
		t.Fatalf("Марко види туђе Кешкиће: %+v", markoMonth.Summary)
	}

	// Марко не може обрисати Админов Кешкић.
	delForeign := httptest.NewRequest(http.MethodDelete, "/api/keshkici/"+strconv.FormatInt(admK.ID, 10), nil)
	delForeign.AddCookie(markoCookie)
	if r, _ := app.Test(delForeign); r.StatusCode != http.StatusNotFound {
		t.Fatalf("очекивано 404 за туђи Кешкић, добијено %d", r.StatusCode)
	}

	// Увоз резервне копије код Марка са ИД=1 (који заузима Админ) пролази без грешке и не дира Админа!
	backupCollision := `{
		"app": "keshk",
		"version": 1,
		"keshkici": [{"id": ` + strconv.FormatInt(admK.ID, 10) + `, "month": "2026-11", "title": "Марков Кешкић", "amount": 9000, "currency": "RSD", "rate": 1, "isDefault": true}],
		"transactions": [{"id": 1, "keshkicId": ` + strconv.FormatInt(admK.ID, 10) + `, "date": "2026-11-02", "title": "Ужина", "amount": 450, "currency": "RSD", "rate": 1}],
		"lists": []
	}`
	impReq := httptest.NewRequest(http.MethodPost, "/api/import", bytes.NewBufferString(backupCollision))
	impReq.Header.Set("Content-Type", "application/json")
	impReq.AddCookie(markoCookie)
	impRes, err := app.Test(impReq)
	if err != nil || impRes.StatusCode != http.StatusOK {
		t.Fatalf("увоз са колизијом ИД-јева није успео: %v %d", err, impRes.StatusCode)
	}

	admExport, err := s.ExportBackup(1)
	if err != nil || len(admExport.Keshkici) != 1 || admExport.Keshkici[0].Title != "Админ Буџет" {
		t.Fatalf("Админови подаци су оштећени приликом Марковог увоза: %+v", admExport)
	}
	markoExport, err := s.ExportBackup(markoMe.ID)
	if err != nil || len(markoExport.Keshkici) != 1 || markoExport.Keshkici[0].Title != "Марков Кешкић" || len(markoExport.Transactions) != 1 {
		t.Fatalf("Маркови подаци након увоза нису исправни: %+v", markoExport)
	}

	// 9. Међусобни преглед профила пријављених корисника.
	viewAdm := httptest.NewRequest(http.MethodGet, "/api/users/@admin", nil)
	viewAdm.AddCookie(markoCookie)
	viewAdmRes, _ := app.Test(viewAdm)
	var admProf userProfile
	_ = json.NewDecoder(viewAdmRes.Body).Decode(&admProf)
	if admProf.IsSelf || !admProf.IsAdmin {
		t.Fatalf("Марков преглед @admin профила неочекиван: %+v", admProf)
	}

	// 10. Админ не може обрисати себе, али може обрисати Марка (чиме се Маркова сесија одмах гаси).
	delSelf := httptest.NewRequest(http.MethodDelete, "/api/admin/users/1", nil)
	delSelf.AddCookie(admCookie)
	if r, _ := app.Test(delSelf); r.StatusCode != http.StatusBadRequest {
		t.Fatalf("очекивано 400 при покушају брисања админа, добијено %d", r.StatusCode)
	}

	delMarko := httptest.NewRequest(http.MethodDelete, "/api/admin/users/"+strconv.FormatInt(markoMe.ID, 10), nil)
	delMarko.AddCookie(admCookie)
	if r, _ := app.Test(delMarko); r.StatusCode != http.StatusNoContent {
		t.Fatalf("брисање корисника није успело: %d", r.StatusCode)
	}

	meAfterDel := httptest.NewRequest(http.MethodGet, "/api/me", nil)
	meAfterDel.AddCookie(markoCookie)
	if r, _ := app.Test(meAfterDel); r.StatusCode != http.StatusUnauthorized {
		t.Fatalf("сесија обрисаног корисника мора вратити 401, добијено %d", r.StatusCode)
	}
}

func TestLoginAndInviteRateLimits(t *testing.T) {
	s := openTestStore(t)
	app, _, adm := buildTestApp(t, s)

	validInvite, err := s.CreateInviteCode(adm.ID)
	if err != nil {
		t.Fatal(err)
	}

	// 1. Rate limit на Login (5 неуспешних -> 6. враћа 429 Too Many Requests).
	for i := 1; i <= 5; i++ {
		req := httptest.NewRequest(http.MethodPost, "/api/login", bytes.NewBufferString(`{"username":"admin","password":"wrong"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Cf-Connecting-Ip", "203.0.113.10")
		res, err := app.Test(req, fiber.TestConfig{Timeout: 5 * time.Second})
		if err != nil || res.StatusCode != http.StatusUnauthorized {
			t.Fatalf("покушај пријаве #%d очекивао 401, добио %v %d", i, err, res.StatusCode)
		}
	}
	blockedLogin := httptest.NewRequest(http.MethodPost, "/api/login", bytes.NewBufferString(`{"username":"admin","password":"tajna123"}`))
	blockedLogin.Header.Set("Content-Type", "application/json")
	blockedLogin.Header.Set("Cf-Connecting-Ip", "203.0.113.10")
	blockedLoginRes, _ := app.Test(blockedLogin)
	if blockedLoginRes.StatusCode != http.StatusTooManyRequests || blockedLoginRes.Header.Get("Retry-After") == "" {
		t.Fatalf("6. покушај пријаве очекивао 429 са Retry-After, добио %d", blockedLoginRes.StatusCode)
	}

	// 2. Rate limit на код за регистрацију (5 погрешних кодова -> 6. враћа 429 чак и уз исправан код).
	for i := 1; i <= 5; i++ {
		req := httptest.NewRequest(http.MethodPost, "/api/register/verify-code", bytes.NewBufferString(`{"code":"BBBB-CCCC-DDDD"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Cf-Connecting-Ip", "203.0.113.20")
		res, err := app.Test(req, fiber.TestConfig{Timeout: 5 * time.Second})
		if err != nil || res.StatusCode != http.StatusBadRequest {
			t.Fatalf("покушај кода #%d очекивао 400, добио %v %d", i, err, res.StatusCode)
		}
	}
	blockedInvite := httptest.NewRequest(http.MethodPost, "/api/register/verify-code", bytes.NewBufferString(`{"code":"`+validInvite.Code+`"}`))
	blockedInvite.Header.Set("Content-Type", "application/json")
	blockedInvite.Header.Set("Cf-Connecting-Ip", "203.0.113.20")
	blockedInviteRes, _ := app.Test(blockedInvite)
	if blockedInviteRes.StatusCode != http.StatusTooManyRequests || blockedInviteRes.Header.Get("Retry-After") == "" {
		t.Fatalf("6. покушај кода очекивао 429 са Retry-After, добио %d", blockedInviteRes.StatusCode)
	}
}


func TestClearDataAndSingleUseDeletionCode(t *testing.T) {
	s := openTestStore(t)
	app, _, adm := buildTestApp(t, s)

	// Пријава администратора и креирање другог корисника (ana).
	admLogin := httptest.NewRequest(http.MethodPost, "/api/login", bytes.NewBufferString(`{"username":"admin","password":"tajna123"}`))
	admLogin.Header.Set("Content-Type", "application/json")
	admLoginRes, err := app.Test(admLogin)
	if err != nil || admLoginRes.StatusCode != http.StatusOK {
		t.Fatalf("админ пријава: %v %d", err, admLoginRes.StatusCode)
	}
	admCookie := sessionCookieFrom(admLoginRes)

	inv, err := s.CreateInviteCode(adm.ID)
	if err != nil {
		t.Fatal(err)
	}
	regReq := httptest.NewRequest(http.MethodPost, "/api/register", bytes.NewBufferString(`{"code":"`+inv.Code+`","username":"ana","password":"anaPassword1","displayName":"Ана"}`))
	regReq.Header.Set("Content-Type", "application/json")
	regRes, err := app.Test(regReq)
	if err != nil || regRes.StatusCode != http.StatusCreated {
		t.Fatalf("регистрација Ане: %v %d", err, regRes.StatusCode)
	}
	var anaMe accountInfo
	_ = json.NewDecoder(regRes.Body).Decode(&anaMe)
	anaCookie := sessionCookieFrom(regRes)

	// Подаци код админа (да проверимо да Анино брисање података НЕ дира админа).
	if _, err := s.CreateShoppingList(adm.ID, ShoppingList{Title: "Админова листа", Currency: "RSD"}); err != nil {
		t.Fatal(err)
	}

	// Подаци код Ане: Кешкић + трошак + листа са ставком.
	anaK, err := s.CreateKeshkic(anaMe.ID, Keshkic{
		Month: "2026-11", Title: "Плата", Amount: 20000, Currency: "RSD", Rate: 1, AmountRSD: 20000, IsDefault: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateTx(anaMe.ID, Tx{
		KeshkicID: anaK.ID, Month: "2026-11", Date: "2026-11-02", Title: "Књига", Amount: 1200, Currency: "RSD", Rate: 1, AmountRSD: 1200,
	}); err != nil {
		t.Fatal(err)
	}
	anaList, err := s.CreateShoppingList(anaMe.ID, ShoppingList{Title: "Пијаца", KeshkicID: anaK.ID, Currency: "RSD"})
	if err != nil {
		t.Fatal(err)
	}
	price := 300.0
	if err := s.AddShoppingItem(anaMe.ID, anaList.ID, "Јабуке", &price); err != nil {
		t.Fatal(err)
	}

	// 1. Празан избор података се одбија са 400.
	emptyClr := httptest.NewRequest(http.MethodPost, "/api/account/clear-data", bytes.NewBufferString(`{"lists":false,"transactions":false,"keshkici":false}`))
	emptyClr.Header.Set("Content-Type", "application/json")
	emptyClr.AddCookie(anaCookie)
	if r, _ := app.Test(emptyClr); r.StatusCode != http.StatusBadRequest {
		t.Fatalf("празан clear-data очекивао 400, добио %d", r.StatusCode)
	}

	// 2. Брисање САМО листа код Ане — листе се бришу, Кешкић и трошак остају, админ остаје нетакнут.
	clrLists := httptest.NewRequest(http.MethodPost, "/api/account/clear-data", bytes.NewBufferString(`{"lists":true}`))
	clrLists.Header.Set("Content-Type", "application/json")
	clrLists.AddCookie(anaCookie)
	if r, err := app.Test(clrLists); err != nil || r.StatusCode != http.StatusOK {
		t.Fatalf("брисање листа није успело: %v %d", err, r.StatusCode)
	}
	anaExp1, _ := s.ExportBackup(anaMe.ID)
	if len(anaExp1.Lists) != 0 || len(anaExp1.Keshkici) != 1 || len(anaExp1.Transactions) != 1 {
		t.Fatalf("након брисања листа неочекивано стање код Ане: %+v", anaExp1)
	}
	admLists, _ := s.ListShoppingLists(adm.ID)
	if len(admLists) != 1 {
		t.Fatalf("Админове листе не смеју бити обрисане, добијено %d", len(admLists))
	}

	// 3. Брисање Кешкића код Ане брише и трошкове, али налог Ане остаје пријављен.
	clrAll := httptest.NewRequest(http.MethodPost, "/api/account/clear-data", bytes.NewBufferString(`{"keshkici":true}`))
	clrAll.Header.Set("Content-Type", "application/json")
	clrAll.AddCookie(anaCookie)
	if r, err := app.Test(clrAll); err != nil || r.StatusCode != http.StatusOK {
		t.Fatalf("брисање Кешкића није успело: %v %d", err, r.StatusCode)
	}
	anaExp2, _ := s.ExportBackup(anaMe.ID)
	if len(anaExp2.Keshkici) != 0 || len(anaExp2.Transactions) != 0 {
		t.Fatalf("очекивани празни Кешкићи и трошкови, добијено: %+v", anaExp2)
	}

	// 4. Главни админ не може генерисати код за брисање сопственог налога.
	admDelCode := httptest.NewRequest(http.MethodPost, "/api/account/deletion-code", nil)
	admDelCode.AddCookie(admCookie)
	if r, _ := app.Test(admDelCode); r.StatusCode != http.StatusBadRequest {
		t.Fatalf("админ генерисање кода за брисање очекивало 400, добило %d", r.StatusCode)
	}

	// 5. Ана генерише потврдни код за брисање налога ПРВИ ПУТ -> 201 Created.
	gen1 := httptest.NewRequest(http.MethodPost, "/api/account/deletion-code", nil)
	gen1.AddCookie(anaCookie)
	gen1Res, err := app.Test(gen1)
	if err != nil || gen1Res.StatusCode != http.StatusCreated {
		t.Fatalf("прво генерисање кода за брисање није успело: %v %d", err, gen1Res.StatusCode)
	}
	var anaWithCode accountInfo
	_ = json.NewDecoder(gen1Res.Body).Decode(&anaWithCode)
	if len(anaWithCode.DeletionCode) != 14 {
		t.Fatalf("неочекиван код за брисање: %q", anaWithCode.DeletionCode)
	}
	firstCode := anaWithCode.DeletionCode

	// 6. Други покушај генерисања кода док админ не потврди МОРА вратити 409 Conflict, а стари код остаје исти!
	gen2 := httptest.NewRequest(http.MethodPost, "/api/account/deletion-code", nil)
	gen2.AddCookie(anaCookie)
	gen2Res, _ := app.Test(gen2)
	if gen2Res.StatusCode != http.StatusConflict {
		t.Fatalf("други покушај генерисања кода очекивао 409, добио %d", gen2Res.StatusCode)
	}
	meCheck := httptest.NewRequest(http.MethodGet, "/api/me", nil)
	meCheck.AddCookie(anaCookie)
	meCheckRes, _ := app.Test(meCheck)
	var anaCheck accountInfo
	_ = json.NewDecoder(meCheckRes.Body).Decode(&anaCheck)
	if anaCheck.DeletionCode != firstCode {
		t.Fatalf("код за брисање се променио са %q на %q", firstCode, anaCheck.DeletionCode)
	}

	// Јавни профил (/api/users/@ana) не открива код за брисање другим корисницима.
	pubProfReq := httptest.NewRequest(http.MethodGet, "/api/users/@ana", nil)
	pubProfReq.AddCookie(admCookie)
	pubProfRes, _ := app.Test(pubProfReq)
	var rawProf map[string]any
	_ = json.NewDecoder(pubProfRes.Body).Decode(&rawProf)
	if _, exposed := rawProf["deletionCode"]; exposed {
		t.Fatalf("deletionCode не сме бити видљив на /api/users/@ana: %+v", rawProf)
	}

	// 7. Админ види Анин захтев и тачан КОД у /api/admin/overview.
	ovReq := httptest.NewRequest(http.MethodGet, "/api/admin/overview", nil)
	ovReq.AddCookie(admCookie)
	ovRes, _ := app.Test(ovReq)
	var ov struct {
		Users []User `json:"users"`
	}
	_ = json.NewDecoder(ovRes.Body).Decode(&ov)
	var foundCode string
	for _, u := range ov.Users {
		if u.ID == anaMe.ID {
			foundCode = u.DeletionCode
		}
	}
	if foundCode != firstCode {
		t.Fatalf("админ није видео очекивани deletionCode %q, добио %q", firstCode, foundCode)
	}

	// 8. Ако админ поништи код (/api/admin/users/:id/deletion-code), Ана може поново да га генерише једном.
	clrCodeReq := httptest.NewRequest(http.MethodDelete, "/api/admin/users/"+strconv.FormatInt(anaMe.ID, 10)+"/deletion-code", nil)
	clrCodeReq.AddCookie(admCookie)
	if r, _ := app.Test(clrCodeReq); r.StatusCode != http.StatusNoContent {
		t.Fatalf("поништавање кода од стране админа очекивало 204, добило %d", r.StatusCode)
	}
	gen3 := httptest.NewRequest(http.MethodPost, "/api/account/deletion-code", nil)
	gen3.AddCookie(anaCookie)
	if r, _ := app.Test(gen3); r.StatusCode != http.StatusCreated {
		t.Fatalf("након што је админ поништио код, ново генерисање очекивало 201, добило %d", r.StatusCode)
	}
}


func TestAdminSearchPaginationAndIndexes(t *testing.T) {
	s := openTestStore(t)
	app, _, adm := buildTestApp(t, s)

	admLogin := httptest.NewRequest(http.MethodPost, "/api/login", bytes.NewBufferString(`{"username":"admin","password":"tajna123"}`))
	admLogin.Header.Set("Content-Type", "application/json")
	admLoginRes, err := app.Test(admLogin)
	if err != nil || admLoginRes.StatusCode != http.StatusOK {
		t.Fatalf("админ пријава: %v %d", err, admLoginRes.StatusCode)
	}
	admCookie := sessionCookieFrom(admLoginRes)

	// Креирај још 19 корисника (укупно 20 -> страна 1 = 15, страна 2 = 5).
	var nikolaID, page2DelID int64
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	for i := 1; i <= 19; i++ {
		inv, err := s.CreateInviteCode(adm.ID)
		if err != nil {
			t.Fatal(err)
		}
		uname := "korisnik" + strconv.Itoa(i)
		dname := "Члан " + strconv.Itoa(i)
		if i == 17 {
			uname = "nikola_bg"
			dname = "Никола Београд"
		}
		if i == 18 {
			uname = "nikolaxbg"
			dname = "Други"
		}
		u, err := s.RegisterWithInvite(registerParams{
			Code:        inv.Code,
			Username:    uname,
			Password:    "lozinka123",
			DisplayName: dname,
		}, now)
		if err != nil {
			t.Fatalf("регистрација #%d: %v", i, err)
		}
		if i == 17 {
			nikolaID = u.ID
		}
		if i == 19 {
			page2DelID = u.ID
		}
	}

	// Корисник са 2. стране генерише код за брисање — админ мора видети захтев већ на 1. страни!
	if _, err := s.GenerateDeletionCode(page2DelID, now); err != nil {
		t.Fatal(err)
	}

	type overviewResp struct {
		Stats            AdminStats   `json:"stats"`
		PendingDeletions []User       `json:"pendingDeletions"`
		Users            []User       `json:"users"`
		UsersMeta        PageMeta     `json:"usersMeta"`
		Invites          []InviteCode `json:"invites"`
		InvitesMeta      PageMeta     `json:"invitesMeta"`
	}

	// 1. Страна 1 враћа тачно 15 корисника и 15 позивница, док stats приказује свих 20.
	p1Req := httptest.NewRequest(http.MethodGet, "/api/admin/overview?page=1&invitesPage=1", nil)
	p1Req.AddCookie(admCookie)
	p1Res, err := app.Test(p1Req)
	if err != nil || p1Res.StatusCode != http.StatusOK {
		t.Fatalf("страна 1 није успела: %v %d", err, p1Res.StatusCode)
	}
	var p1 overviewResp
	if err := json.NewDecoder(p1Res.Body).Decode(&p1); err != nil {
		t.Fatal(err)
	}
	if len(p1.Users) != 15 || p1.UsersMeta.Total != 20 || p1.UsersMeta.TotalPages != 2 || p1.Stats.TotalUsers != 20 {
		t.Fatalf("неочекивана пагинација стране 1: len=%d meta=%+v stats=%+v", len(p1.Users), p1.UsersMeta, p1.Stats)
	}
	if len(p1.Invites) != 15 || p1.InvitesMeta.Total != 19 || p1.InvitesMeta.TotalPages != 2 {
		t.Fatalf("неочекивана пагинација позивница: len=%d meta=%+v", len(p1.Invites), p1.InvitesMeta)
	}
	if len(p1.PendingDeletions) != 1 || p1.PendingDeletions[0].ID != page2DelID {
		t.Fatalf("захтев за брисање са стране 2 мора бити у pendingDeletions: %+v", p1.PendingDeletions)
	}

	// 2. Страна 2 враћа преосталих 5 корисника.
	p2Req := httptest.NewRequest(http.MethodGet, "/api/admin/overview?page=2&invitesPage=2", nil)
	p2Req.AddCookie(admCookie)
	p2Res, _ := app.Test(p2Req)
	var p2 overviewResp
	_ = json.NewDecoder(p2Res.Body).Decode(&p2)
	if len(p2.Users) != 5 || p2.UsersMeta.Page != 2 || len(p2.Invites) != 4 {
		t.Fatalf("неочекивана страна 2: users=%d invites=%d meta=%+v", len(p2.Users), len(p2.Invites), p2.UsersMeta)
	}

	// 3. Претрага по @nikola_bg (са доњом цртом ескејпованом да НЕ ухвати nikolaxbg!), по ћириличном имену и по #ID.
	for _, q := range []string{"@nikola_bg", "Београд", "#" + strconv.FormatInt(nikolaID, 10)} {
		sReq := httptest.NewRequest(http.MethodGet, "/api/admin/overview?q="+url.QueryEscape(q), nil)
		sReq.AddCookie(admCookie)
		sRes, _ := app.Test(sReq)
		var sr overviewResp
		_ = json.NewDecoder(sRes.Body).Decode(&sr)
		if len(sr.Users) != 1 || sr.Users[0].ID != nikolaID {
			t.Fatalf("претрага за %q очекивала само nikola_bg (id=%d), добила %+v", q, nikolaID, sr.Users)
		}
	}

	// 4. Провера да су сви кључни индекси заиста креирани у Postgres шеми.
	wantIndexes := []string{
		"idx_users_username_lower",
		"idx_users_admin_id",
		"idx_users_display_lower",
		"idx_users_pending_deletion",
		"idx_invites_active_id",
		"idx_invites_used_by",
		"idx_keshkici_user_month",
		"idx_keshkici_user_active_def",
		"idx_tx_user_month_date",
		"idx_tx_keshkic",
		"idx_tx_user_keshkic_month",
		"idx_lists_user_order",
		"idx_lists_keshkic",
		"idx_lists_tx",
		"idx_items_list_pos",
	}
	for _, idxName := range wantIndexes {
		var reg sql.NullString
		if err := s.db.QueryRow(`SELECT to_regclass($1)`, idxName).Scan(&reg); err != nil || !reg.Valid {
			t.Fatalf("недостаје очекивани Postgres индекс %q (err=%v)", idxName, err)
		}
	}
}
