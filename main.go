package main

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"io/fs"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/logger"
	recoverer "github.com/gofiber/fiber/v3/middleware/recover"
	"github.com/gofiber/fiber/v3/middleware/static"
	"github.com/joho/godotenv"
)

//go:embed web
var webFS embed.FS

func runHealthcheck() int {
	_ = godotenv.Load()
	port := env("PORT", "3000")
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get("http://127.0.0.1:" + port + "/healthz")
	if err != nil {
		return 1
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 1
	}
	return 0
}

func main() {
	if len(os.Args) > 1 && (os.Args[1] == "-healthcheck" || os.Args[1] == "--healthcheck") {
		os.Exit(runHealthcheck())
	}

	cfg := loadConfig()

	store, err := openStore(cfg.DatabaseURL, cfg.DatabaseURLUnpooled)
	if err != nil {
		log.Fatalf("[ERROR] база: %v", err)
	}
	defer store.Close()

	now := time.Now()
	adminUser, err := store.EnsureAdminUser(cfg, now)
	if err != nil {
		log.Fatalf("[ERROR] иницијализација администратора: %v", err)
	}

	if cfg.Seed {
		if err := seedDemo(store, adminUser.ID, now); err != nil {
			log.Fatalf("[ERROR] пробни подаци: %v", err)
		}
	}

	h := &Handlers{
		cfg:           cfg,
		store:         store,
		rates:         newRates(cfg.FallbackRates),
		auth:          newAuth(cfg, store),
		limiter:       newLoginLimiter(),
		inviteLimiter: newInviteLimiter(),
	}

	app := fiber.New(fiber.Config{AppName: "Keshk"})
	app.Use(recoverer.New())
	app.Get("/healthz", func(c fiber.Ctx) error {
		return c.SendString("ok")
	})
	app.Use(logger.New())
	app.Use(func(c fiber.Ctx) error {
		c.Set("Cache-Control", "no-store, no-cache, must-revalidate, max-age=0")
		c.Set("Pragma", "no-cache")
		c.Set("Expires", "0")
		return c.Next()
	})

	api := app.Group("/api", h.auth.Middleware)
	api.Post("/login", h.Login)
	api.Post("/logout", h.Logout)
	api.Post("/register/verify-code", h.VerifyInviteCode)
	api.Post("/register/check-username", h.CheckUsername)
	api.Post("/register", h.Register)

	api.Get("/me", h.Me)
	api.Get("/rates", h.Rates)
	api.Get("/history", h.History)
	api.Get("/months/:month", h.Month)
	api.Post("/keshkici", h.CreateKeshkic)
	api.Put("/keshkici/:id", h.UpdateKeshkic)
	api.Put("/keshkici/:id/default", h.SetDefaultKeshkic)
	api.Put("/keshkici/:id/drop", h.DropKeshkic)
	api.Delete("/keshkici/:id", h.DeleteKeshkic)
	api.Post("/transactions", h.CreateTx)
	api.Put("/transactions/:id", h.UpdateTx)
	api.Delete("/transactions/:id", h.DeleteTx)

	// Налог, подешавања и профили
	api.Put("/account", h.UpdateAccount)
	api.Put("/account/password", h.ChangePassword)
	api.Post("/account/clear-data", h.ClearAccountData)
	api.Post("/account/deletion-code", h.CreateDeletionCode)
	api.Get("/users/:handle/qr", h.GetUserQR)
	api.Get("/users/:handle", h.GetUser)

	// Админ панел (само први налог)
	adminAPI := api.Group("/admin", h.auth.AdminMiddleware)
	adminAPI.Get("/overview", h.AdminOverview)
	adminAPI.Post("/invites", h.AdminCreateInvite)
	adminAPI.Delete("/invites/:id", h.AdminDeleteInvite)
	adminAPI.Delete("/users/:id/deletion-code", h.AdminClearDeletionCode)
	adminAPI.Delete("/users/:id", h.AdminDeleteUser)

	// Листе за куповину
	api.Get("/lists", h.ListLists)
	api.Post("/lists", h.CreateList)
	api.Get("/lists/:id", h.GetList)
	api.Put("/lists/:id", h.UpdateList)
	api.Delete("/lists/:id", h.DeleteList)
	api.Post("/lists/:id/items", h.AddItem)
	api.Put("/lists/:id/items/:itemId", h.UpdateItem)
	api.Delete("/lists/:id/items/:itemId", h.DeleteItem)
	api.Post("/lists/:id/charge", h.ChargeList)
	api.Delete("/lists/:id/charge", h.UnchargeList)

	// Извоз / увоз података
	api.Get("/export", h.ExportData)
	api.Post("/import", h.ImportData)

	api.Use(func(c fiber.Ctx) error {
		return fail(c, fiber.StatusNotFound, "Рута не постоји")
	})

	indexHTML, err := webFS.ReadFile("web/index.html")
	if err != nil {
		log.Fatal(err)
	}
	// Верзија асета = хеш садржаја, да прегледач никада не користи застарели CSS/JS из кеша
	h256 := sha256.New()
	for _, name := range []string{"web/styles.css", "web/app.js"} {
		b, _ := webFS.ReadFile(name)
		h256.Write(b)
	}
	ver := hex.EncodeToString(h256.Sum(nil))[:10]
	indexHTML = bytes.ReplaceAll(indexHTML, []byte(`"/styles.css"`), []byte(`"/styles.css?v=`+ver+`"`))
	indexHTML = bytes.ReplaceAll(indexHTML, []byte(`"/app.js"`), []byte(`"/app.js?v=`+ver+`"`))
	sendIndex := func(c fiber.Ctx) error {
		c.Set("Cache-Control", "no-store, no-cache, must-revalidate, max-age=0")
		c.Set("Pragma", "no-cache")
		c.Set("Expires", "0")
		c.Set("Content-Type", "text/html; charset=utf-8")
		return c.Send(indexHTML)
	}
	app.Get("/", sendIndex)
	app.Get("/index.html", sendIndex)
	app.Get("/liste", sendIndex)
	app.Get("/liste/*", sendIndex)
	app.Get("/u", sendIndex)
	app.Get("/u/*", sendIndex)
	app.Get("/podesavanja", sendIndex)
	app.Get("/podesavanja/*", sendIndex)
	app.Get("/admin", sendIndex)
	app.Get("/admin/*", sendIndex)

	web, err := fs.Sub(webFS, "web")
	if err != nil {
		log.Fatal(err)
	}
	app.Get("/*", static.New("", static.Config{
		FS:            web,
		MaxAge:        -1,
		CacheDuration: -1,
		ModifyResponse: func(c fiber.Ctx) error {
			c.Set("Cache-Control", "no-store, no-cache, must-revalidate, max-age=0")
			c.Set("Pragma", "no-cache")
			c.Set("Expires", "0")
			return nil
		},
	}))
	app.Get("/*", sendIndex)

	log.Printf("[INFO] Keshk radi na http://localhost:%s", cfg.Port)
	log.Fatal(app.Listen(":" + cfg.Port))
}
