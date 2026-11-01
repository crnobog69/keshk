package main

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"io/fs"
	"log"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/logger"
	recoverer "github.com/gofiber/fiber/v3/middleware/recover"
	"github.com/gofiber/fiber/v3/middleware/static"
)

//go:embed web
var webFS embed.FS

func main() {
	cfg := loadConfig()

	store, err := openStore(cfg.DBPath)
	if err != nil {
		log.Fatalf("[ERROR] база: %v", err)
	}
	defer store.Close()

	if cfg.ResetPassword {
		if err := store.SetSetting(settingPassHash, ""); err == nil {
			log.Println("[INFO] Лозинка враћена на вредност из .env (KESHK_RESET_PASSWORD)")
		}
	}

	if cfg.Seed {
		if err := seedDemo(store, time.Now()); err != nil {
			log.Fatalf("[ERROR] пробни подаци: %v", err)
		}
	}
	store.EnsureMemberSince(time.Now())

	h := &Handlers{cfg: cfg, store: store, rates: newRates(cfg.FallbackRates), auth: newAuth(cfg, store.GetSetting(settingPassHash)), limiter: newLoginLimiter()}

	app := fiber.New(fiber.Config{AppName: "Keshk"})
	app.Use(recoverer.New())
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

	// Налог и профили
	api.Put("/account", h.UpdateAccount)
	api.Put("/account/password", h.ChangePassword)
	api.Get("/users/:handle", h.GetUser)

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
