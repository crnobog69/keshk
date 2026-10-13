package main

import (
	"embed"
	"io/fs"
	"log"

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
		log.Fatalf("❌ база: %v", err)
	}
	defer store.Close()

	h := &Handlers{cfg: cfg, store: store, rates: newRates(cfg.FallbackRates), auth: newAuth(cfg)}

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
	api.Use(func(c fiber.Ctx) error {
		return fail(c, fiber.StatusNotFound, "Рута не постоји")
	})

	indexHTML, err := webFS.ReadFile("web/index.html")
	if err != nil {
		log.Fatal(err)
	}
	sendIndex := func(c fiber.Ctx) error {
		c.Set("Cache-Control", "no-store, no-cache, must-revalidate, max-age=0")
		c.Set("Pragma", "no-cache")
		c.Set("Expires", "0")
		c.Set("Content-Type", "text/html; charset=utf-8")
		return c.Send(indexHTML)
	}
	app.Get("/", sendIndex)
	app.Get("/index.html", sendIndex)

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

	log.Printf("[INFO] Keshk radi na http://localhost:%s", cfg.Port)
	log.Fatal(app.Listen(":" + cfg.Port))
}
