package main

import (
	"errors"
	"log"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
)

type Handlers struct {
	cfg           Config
	store         *Store
	rates         *Rates
	auth          *Auth
	limiter       *RateLimiter
	inviteLimiter *RateLimiter
}

type MonthSummary struct {
	Month            string    `json:"month"`
	TotalKeshkic     float64   `json:"totalKeshkic"`
	Expenses         float64   `json:"expenses"`
	Balance          float64   `json:"balance"`
	Keshkici         []Keshkic `json:"keshkici"`
	DefaultKeshkicID int64     `json:"defaultKeshkicId"`
	TxCount          int       `json:"txCount"`

	// Резервна компатибилност за старе/кеширане клијенте
	Allowance        float64 `json:"allowance"`
	TotalIncome      float64 `json:"totalIncome"`
	Total            float64 `json:"total"`
	DefaultAllowance float64 `json:"defaultAllowance"`
	CustomAllowance  bool    `json:"customAllowance"`
}

type MonthHistoryItem struct {
	Month        string  `json:"month"`
	TotalKeshkic float64 `json:"totalKeshkic"`
	Expenses     float64 `json:"expenses"`
	Balance      float64 `json:"balance"`
	TxCount      int     `json:"txCount"`
}

var monthRe = regexp.MustCompile(`^\d{4}-(0[1-9]|1[0-2])$`)

func validMonth(m string) bool { return monthRe.MatchString(m) }
func round2(v float64) float64 { return math.Round(v*100) / 100 }

func fail(c fiber.Ctx, status int, msg string) error {
	return c.Status(status).JSON(fiber.Map{"error": msg})
}

func internal(c fiber.Ctx, err error) error {
	log.Printf("[ERROR] %s %s: %v", c.Method(), c.Path(), err)
	return fail(c, fiber.StatusInternalServerError, "Грешка на серверу")
}

// ---------------------------------------------------------------------
// Auth
// ---------------------------------------------------------------------

func (h *Handlers) Login(c fiber.Ctx) error {
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := c.Bind().JSON(&body); err != nil {
		return fail(c, fiber.StatusBadRequest, "Неисправан захтев")
	}
	ip := clientIP(c)
	if blocked, wait := h.limiter.blocked(ip); blocked {
		return rejectRateLimited(c, wait)
	}
	u, ok := h.store.AuthenticateUser(strings.TrimSpace(body.Username), body.Password)
	if !ok {
		h.limiter.fail(ip)
		log.Printf("[WARN] неуспешна пријава са %s", ip)
		time.Sleep(400 * time.Millisecond)
		return fail(c, fiber.StatusUnauthorized, "Погрешно корисничко име или лозинка")
	}
	h.limiter.success(ip)
	h.auth.setSession(c, u)
	return c.JSON(fiber.Map{
		"username": u.Username,
		"isAdmin":  u.IsAdmin,
	})
}

func (h *Handlers) Logout(c fiber.Ctx) error {
	h.auth.clearSession(c)
	return c.SendStatus(fiber.StatusNoContent)
}

// ---------------------------------------------------------------------
// Months & Summary
// ---------------------------------------------------------------------

func (h *Handlers) summary(userID int64, month string) (MonthSummary, error) {
	keshkici, err := h.store.ListKeshkici(userID, month)
	if err != nil {
		return MonthSummary{}, err
	}

	totals, err := h.store.MonthTotals(userID, month)
	if err != nil {
		return MonthSummary{}, err
	}

	var defID int64
	for _, k := range keshkici {
		if k.IsDefault && !k.Dropped {
			defID = k.ID
			break
		}
	}
	if defID == 0 {
		for _, k := range keshkici {
			if !k.Dropped {
				defID = k.ID
				break
			}
		}
	}

	totalVal := round2(totals.TotalKeshkic)
	return MonthSummary{
		Month:            month,
		TotalKeshkic:     totalVal,
		Expenses:         round2(totals.Expenses),
		Balance:          round2(totals.Balance),
		Keshkici:         keshkici,
		DefaultKeshkicID: defID,
		TxCount:          totals.TxCount,
		Allowance:        totalVal,
		TotalIncome:      totalVal,
		Total:            totalVal,
		DefaultAllowance: h.cfg.DefaultKeshkic,
		CustomAllowance:  len(keshkici) > 0,
	}, nil
}

func (h *Handlers) Month(c fiber.Ctx) error {
	uid := currentUser(c).ID
	month := c.Params("month")
	if !validMonth(month) {
		return fail(c, fiber.StatusBadRequest, "Неисправан месец")
	}

	sum, err := h.summary(uid, month)
	if err != nil {
		return internal(c, err)
	}

	var keshkicFilter int64
	if kIDStr := c.Query("keshkicId"); kIDStr != "" {
		keshkicFilter, _ = strconv.ParseInt(kIDStr, 10, 64)
	}

	txs, err := h.store.ListTx(uid, month, keshkicFilter)
	if err != nil {
		return internal(c, err)
	}

	return c.JSON(fiber.Map{
		"summary":      sum,
		"transactions": txs,
	})
}

func (h *Handlers) History(c fiber.Ctx) error {
	uid := currentUser(c).ID
	end := c.Query("end")
	if !validMonth(end) {
		end = time.Now().Format("2006-01")
	}
	n, err := strconv.Atoi(c.Query("n", "6"))
	if err != nil || n < 1 || n > 24 {
		n = 6
	}
	t, _ := time.Parse("2006-01", end)
	out := make([]MonthHistoryItem, 0, n)
	for i := n - 1; i >= 0; i-- {
		m := t.AddDate(0, -i, 0).Format("2006-01")
		totals, err := h.store.MonthTotals(uid, m)
		if err != nil {
			return internal(c, err)
		}
		out = append(out, MonthHistoryItem{
			Month:        m,
			TotalKeshkic: round2(totals.TotalKeshkic),
			Expenses:     round2(totals.Expenses),
			Balance:      round2(totals.Balance),
			TxCount:      totals.TxCount,
		})
	}
	return c.JSON(out)
}

// ---------------------------------------------------------------------
// Keshkici CRUD
// ---------------------------------------------------------------------

type keshkicInput struct {
	Month     string  `json:"month"`
	Title     string  `json:"title"`
	Amount    float64 `json:"amount"`
	Currency  string  `json:"currency"`
	Rate      float64 `json:"rate"`
	IsDefault bool    `json:"isDefault"`
}

func (h *Handlers) parseKeshkic(c fiber.Ctx) (Keshkic, error) {
	var in keshkicInput
	if err := c.Bind().JSON(&in); err != nil {
		return Keshkic{}, errors.New("Неисправан захтев")
	}

	in.Month = strings.TrimSpace(in.Month)
	if !validMonth(in.Month) {
		return Keshkic{}, errors.New("Неисправан месец (очекиван YYYY-MM)")
	}

	in.Title = strings.TrimSpace(in.Title)
	if in.Title == "" {
		in.Title = "Кешкић"
	}
	if len([]rune(in.Title)) > 60 {
		return Keshkic{}, errors.New("Назив Кешкића је предугачак")
	}

	if !(in.Amount > 0) || in.Amount > 1e10 {
		return Keshkic{}, errors.New("Износ Кешкића мора бити већи од нуле")
	}

	in.Currency = strings.ToUpper(strings.TrimSpace(in.Currency))
	if in.Currency == "" {
		in.Currency = "RSD"
	}
	if !isSupportedCurrency(in.Currency) {
		return Keshkic{}, errors.New("Неподржана валута")
	}

	rate := in.Rate
	if in.Currency == "RSD" {
		rate = 1
	} else if !(rate > 0) {
		rate = h.rates.Rate(in.Currency)
	}

	return Keshkic{
		Month:     in.Month,
		Title:     in.Title,
		Amount:    round2(in.Amount),
		Currency:  in.Currency,
		Rate:      rate,
		AmountRSD: round2(in.Amount * rate),
		IsDefault: in.IsDefault,
	}, nil
}

func (h *Handlers) CreateKeshkic(c fiber.Ctx) error {
	k, err := h.parseKeshkic(c)
	if err != nil {
		return fail(c, fiber.StatusBadRequest, err.Error())
	}
	created, err := h.store.CreateKeshkic(currentUser(c).ID, k)
	if err != nil {
		return internal(c, err)
	}
	return c.Status(fiber.StatusCreated).JSON(created)
}

func (h *Handlers) UpdateKeshkic(c fiber.Ctx) error {
	id, err := strconv.ParseInt(c.Params("id"), 10, 64)
	if err != nil {
		return fail(c, fiber.StatusBadRequest, "Неисправан ID")
	}
	k, err := h.parseKeshkic(c)
	if err != nil {
		return fail(c, fiber.StatusBadRequest, err.Error())
	}
	k.ID = id
	updated, err := h.store.UpdateKeshkic(currentUser(c).ID, k)
	if errors.Is(err, errNotFound) {
		return fail(c, fiber.StatusNotFound, "Кешкић не постоји")
	} else if err != nil {
		return internal(c, err)
	}
	return c.JSON(updated)
}

func (h *Handlers) SetDefaultKeshkic(c fiber.Ctx) error {
	uid := currentUser(c).ID
	id, err := strconv.ParseInt(c.Params("id"), 10, 64)
	if err != nil {
		return fail(c, fiber.StatusBadRequest, "Неисправан ID")
	}
	if _, err := h.store.GetKeshkic(uid, id); errors.Is(err, errNotFound) {
		return fail(c, fiber.StatusNotFound, "Кешкић не постоји")
	} else if err != nil {
		return internal(c, err)
	}

	if err := h.store.SetDefaultKeshkic(uid, id); errors.Is(err, errNotFound) {
		return fail(c, fiber.StatusNotFound, "Кешкић не постоји")
	} else if err != nil {
		return internal(c, err)
	}
	return c.JSON(fiber.Map{"ok": true, "defaultKeshkicId": id})
}

// DropKeshkic: PUT /api/keshkici/:id/drop  {"month":"YYYY-MM"} одбацује, {"month":""} враћа у употребу.
func (h *Handlers) DropKeshkic(c fiber.Ctx) error {
	id, err := strconv.ParseInt(c.Params("id"), 10, 64)
	if err != nil {
		return fail(c, fiber.StatusBadRequest, "Неисправан ID")
	}
	var body struct {
		Month string `json:"month"`
	}
	if err := c.Bind().JSON(&body); err != nil {
		return fail(c, fiber.StatusBadRequest, "Неисправан захтев")
	}
	body.Month = strings.TrimSpace(body.Month)
	if body.Month != "" && !validMonth(body.Month) {
		return fail(c, fiber.StatusBadRequest, "Неисправан месец")
	}
	if err := h.store.DropKeshkic(currentUser(c).ID, id, body.Month); errors.Is(err, errNotFound) {
		return fail(c, fiber.StatusNotFound, "Кешкић не постоји")
	} else if err != nil {
		return internal(c, err)
	}
	return c.JSON(fiber.Map{"ok": true})
}

func (h *Handlers) DeleteKeshkic(c fiber.Ctx) error {
	id, err := strconv.ParseInt(c.Params("id"), 10, 64)
	if err != nil {
		return fail(c, fiber.StatusBadRequest, "Неисправан ID")
	}
	if err := h.store.DeleteKeshkic(currentUser(c).ID, id); errors.Is(err, errNotFound) {
		return fail(c, fiber.StatusNotFound, "Кешкић не постоји")
	} else if err != nil {
		return internal(c, err)
	}
	return c.SendStatus(fiber.StatusNoContent)
}

// ---------------------------------------------------------------------
// Transactions (Expenses) CRUD
// ---------------------------------------------------------------------

type txInput struct {
	KeshkicID int64   `json:"keshkicId"`
	Kind      string  `json:"kind"` // "income" (Кешкић) или "expense" (Трошак)
	Date      string  `json:"date"`
	Title     string  `json:"title"`
	Amount    float64 `json:"amount"`
	Currency  string  `json:"currency"`
	Rate      float64 `json:"rate"`
}

func (h *Handlers) parseTxBody(c fiber.Ctx, userID int64) (Tx, string, error) {
	var in txInput
	if err := c.Bind().JSON(&in); err != nil {
		return Tx{}, "", errors.New("Неисправан захтев")
	}

	in.Title = strings.TrimSpace(in.Title)
	if len([]rune(in.Title)) > 120 {
		return Tx{}, "", errors.New("Опис је предугачак")
	}

	if !(in.Amount > 0) || in.Amount > 1e10 {
		return Tx{}, "", errors.New("Износ мора бити већи од нуле")
	}

	in.Currency = strings.ToUpper(strings.TrimSpace(in.Currency))
	if in.Currency == "" {
		in.Currency = "RSD"
	}
	if !isSupportedCurrency(in.Currency) {
		return Tx{}, "", errors.New("Неподржана валута")
	}

	if _, err := time.Parse("2006-01-02", in.Date); err != nil {
		in.Date = time.Now().Format("2006-01-02")
	}

	rate := in.Rate
	if in.Currency == "RSD" {
		rate = 1
	} else if !(rate > 0) {
		rate = h.rates.Rate(in.Currency)
	}

	month := in.Date[:7]

	if in.Kind == "income" {
		return Tx{
			Month:     month,
			Date:      in.Date,
			Title:     in.Title,
			Amount:    round2(in.Amount),
			Currency:  in.Currency,
			Rate:      rate,
			AmountRSD: round2(in.Amount * rate),
		}, in.Kind, nil
	}

	// Ако је трошак, а Кешкић није специфициран, нађи подразумевани за овог корисника
	if in.KeshkicID <= 0 {
		keshkici, _ := h.store.ListKeshkici(userID, month)
		if len(keshkici) > 0 {
			in.KeshkicID = keshkici[0].ID
			for _, k := range keshkici {
				if k.IsDefault {
					in.KeshkicID = k.ID
					break
				}
			}
		} else {
			// Направи почетни Кешкић за тај месец
			newK, err := h.store.CreateKeshkic(userID, Keshkic{
				Month:     month,
				Title:     "Мама",
				Amount:    h.cfg.DefaultKeshkic,
				Currency:  "RSD",
				Rate:      1,
				AmountRSD: h.cfg.DefaultKeshkic,
				IsDefault: true,
			})
			if err != nil {
				return Tx{}, "", err
			}
			in.KeshkicID = newK.ID
		}
	}

	return Tx{
		KeshkicID: in.KeshkicID,
		Month:     month,
		Date:      in.Date,
		Title:     in.Title,
		Amount:    round2(in.Amount),
		Currency:  in.Currency,
		Rate:      rate,
		AmountRSD: round2(in.Amount * rate),
	}, in.Kind, nil
}

func (h *Handlers) CreateTx(c fiber.Ctx) error {
	uid := currentUser(c).ID
	t, kind, err := h.parseTxBody(c, uid)
	if err != nil {
		return fail(c, fiber.StatusBadRequest, err.Error())
	}

	// 1. АКО ЈЕ КОРИСНИК ОДАБРАО "КЕШКИЋ" (kind == "income")
	if kind == "income" {
		title := t.Title
		if title == "" {
			title = "Кешкић"
		}
		created, err := h.store.CreateKeshkic(uid, Keshkic{
			Month:     t.Month,
			Title:     title,
			Amount:    t.Amount,
			Currency:  t.Currency,
			Rate:      t.Rate,
			AmountRSD: t.AmountRSD,
			IsDefault: true,
		})
		if err != nil {
			return internal(c, err)
		}
		return c.Status(fiber.StatusCreated).JSON(created)
	}

	// 2. АКО ЈЕ ТРОШАК
	created, err := h.store.CreateTx(uid, t)
	if err != nil {
		return internal(c, err)
	}
	return c.Status(fiber.StatusCreated).JSON(created)
}

func (h *Handlers) UpdateTx(c fiber.Ctx) error {
	uid := currentUser(c).ID
	id, err := strconv.ParseInt(c.Params("id"), 10, 64)
	if err != nil {
		return fail(c, fiber.StatusBadRequest, "Неисправан ID")
	}
	t, _, err := h.parseTxBody(c, uid)
	if err != nil {
		return fail(c, fiber.StatusBadRequest, err.Error())
	}
	t.ID = id
	updated, err := h.store.UpdateTx(uid, t)
	if errors.Is(err, errNotFound) {
		return fail(c, fiber.StatusNotFound, "Трансакција не постоји")
	} else if err != nil {
		return internal(c, err)
	}
	return c.JSON(updated)
}

func (h *Handlers) DeleteTx(c fiber.Ctx) error {
	id, err := strconv.ParseInt(c.Params("id"), 10, 64)
	if err != nil {
		return fail(c, fiber.StatusBadRequest, "Неисправан ID")
	}
	if err := h.store.DeleteTx(currentUser(c).ID, id); errors.Is(err, errNotFound) {
		return fail(c, fiber.StatusNotFound, "Трансакција не постоји")
	} else if err != nil {
		return internal(c, err)
	}
	return c.SendStatus(fiber.StatusNoContent)
}

func (h *Handlers) Rates(c fiber.Ctx) error {
	return c.JSON(h.rates.Get())
}
