package main

import (
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
)

// ---------------------------------------------------------------------
// Уплате (deposits) — додатни новац на постојећи Кешкић
// ---------------------------------------------------------------------

const depositColumns = `d.id, d.keshkic_id, COALESCE(k.title, 'Непознат'), d.month, d.date, d.title,
	d.amount, d.currency, d.rate, d.amount_rsd, d.created_at`

func scanDeposit(sc interface{ Scan(...any) error }) (Deposit, error) {
	var d Deposit
	err := sc.Scan(&d.ID, &d.KeshkicID, &d.KeshkicTitle, &d.Month, &d.Date, &d.Title,
		&d.Amount, &d.Currency, &d.Rate, &d.AmountRSD, &d.CreatedAt)
	return d, err
}

// ListDeposits враћа уплате корисника у датом месецу (опционо само за један Кешкић).
func (s *Store) ListDeposits(userID int64, month string, keshkicID int64) ([]Deposit, error) {
	query := `
		SELECT ` + depositColumns + `
		FROM deposits d
		LEFT JOIN keshkici k ON k.id = d.keshkic_id AND k.user_id = $1
		WHERE d.user_id = $1 AND d.month = $2`
	args := []any{userID, month}
	if keshkicID > 0 {
		query += ` AND d.keshkic_id = $3`
		args = append(args, keshkicID)
	}
	query += ` ORDER BY d.date DESC, d.id DESC`

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Deposit{}
	for rows.Next() {
		d, err := scanDeposit(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *Store) GetDeposit(userID, id int64) (Deposit, error) {
	d, err := scanDeposit(s.db.QueryRow(`
		SELECT `+depositColumns+`
		FROM deposits d
		LEFT JOIN keshkici k ON k.id = d.keshkic_id AND k.user_id = $2
		WHERE d.id = $1 AND d.user_id = $2`, id, userID))
	if errors.Is(err, sql.ErrNoRows) {
		return d, errNotFound
	}
	return d, err
}

func (s *Store) CreateDeposit(userID int64, d Deposit) (Deposit, error) {
	var id int64
	err := s.db.QueryRow(`
		INSERT INTO deposits (user_id, keshkic_id, month, date, title, amount, currency, rate, amount_rsd)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING id`,
		userID, d.KeshkicID, d.Date[:7], d.Date, d.Title, d.Amount, d.Currency, d.Rate, d.AmountRSD).Scan(&id)
	if err != nil {
		return d, err
	}
	return s.GetDeposit(userID, id)
}

func (s *Store) UpdateDeposit(userID int64, d Deposit) (Deposit, error) {
	res, err := s.db.Exec(`
		UPDATE deposits
		SET keshkic_id = $1, month = $2, date = $3, title = $4, amount = $5, currency = $6, rate = $7, amount_rsd = $8
		WHERE id = $9 AND user_id = $10`,
		d.KeshkicID, d.Date[:7], d.Date, d.Title, d.Amount, d.Currency, d.Rate, d.AmountRSD, d.ID, userID)
	if err != nil {
		return d, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return d, errNotFound
	}
	return s.GetDeposit(userID, d.ID)
}

func (s *Store) DeleteDeposit(userID, id int64) error {
	res, err := s.db.Exec(`DELETE FROM deposits WHERE id = $1 AND user_id = $2`, id, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errNotFound
	}
	return nil
}

// ---------------------------------------------------------------------
// HTTP
// ---------------------------------------------------------------------

type depositInput struct {
	KeshkicID int64   `json:"keshkicId"`
	Date      string  `json:"date"`
	Title     string  `json:"title"`
	Amount    float64 `json:"amount"`
	Currency  string  `json:"currency"`
	Rate      float64 `json:"rate"`
}

func (h *Handlers) parseDeposit(c fiber.Ctx, userID int64) (Deposit, error) {
	var in depositInput
	if err := c.Bind().JSON(&in); err != nil {
		return Deposit{}, errors.New("Неисправан захтев")
	}

	in.Title = strings.TrimSpace(in.Title)
	if len([]rune(in.Title)) > 120 {
		return Deposit{}, errors.New("Опис је предугачак")
	}

	if !(round2(in.Amount) > 0) || in.Amount > 1e10 {
		return Deposit{}, errors.New("Износ уплате мора бити већи од нуле")
	}

	in.Currency = strings.ToUpper(strings.TrimSpace(in.Currency))
	if in.Currency == "" {
		in.Currency = "RSD"
	}
	if !isSupportedCurrency(in.Currency) {
		return Deposit{}, errors.New("Неподржана валута")
	}

	if _, err := time.Parse("2006-01-02", in.Date); err != nil {
		return Deposit{}, errors.New("Изабери исправан датум уплате")
	}

	rate := in.Rate
	if in.Currency == "RSD" {
		rate = 1
	} else if !(rate > 0) {
		rate = h.rates.Rate(in.Currency)
	}
	if !(rate > 0) || math.IsInf(rate, 0) || math.IsNaN(rate) || math.IsInf(in.Amount*rate, 0) {
		return Deposit{}, errors.New("Неисправан курс")
	}

	if in.KeshkicID <= 0 {
		return Deposit{}, errors.New("Изабери Кешкић на који се уплаћује")
	}
	k, err := h.store.GetKeshkic(userID, in.KeshkicID)
	if errors.Is(err, errNotFound) {
		return Deposit{}, errors.New("Изабрани Кешкић не постоји")
	}
	if err != nil {
		return Deposit{}, err
	}
	if in.Date[:7] < k.Month {
		return Deposit{}, fmt.Errorf("Уплата не може бити пре месеца када је Кешкић почео (%s)", k.Month)
	}
	if k.DroppedMonth != "" && in.Date[:7] >= k.DroppedMonth {
		return Deposit{}, errors.New("Не може се уплатити на одбачени Кешкић — прво га врати")
	}

	return Deposit{
		KeshkicID: in.KeshkicID,
		Month:     in.Date[:7],
		Date:      in.Date,
		Title:     in.Title,
		Amount:    round2(in.Amount),
		Currency:  in.Currency,
		Rate:      rate,
		AmountRSD: round2(in.Amount * rate),
	}, nil
}

// CreateDeposit: POST /api/deposits
func (h *Handlers) CreateDeposit(c fiber.Ctx) error {
	uid := currentUser(c).ID
	d, err := h.parseDeposit(c, uid)
	if err != nil {
		return fail(c, fiber.StatusBadRequest, err.Error())
	}
	created, err := h.store.CreateDeposit(uid, d)
	if err != nil {
		return internal(c, err)
	}
	return c.Status(fiber.StatusCreated).JSON(created)
}

// UpdateDeposit: PUT /api/deposits/:id
func (h *Handlers) UpdateDeposit(c fiber.Ctx) error {
	uid := currentUser(c).ID
	id, err := strconv.ParseInt(c.Params("id"), 10, 64)
	if err != nil {
		return fail(c, fiber.StatusBadRequest, "Неисправан ID")
	}
	d, err := h.parseDeposit(c, uid)
	if err != nil {
		return fail(c, fiber.StatusBadRequest, err.Error())
	}
	d.ID = id
	updated, err := h.store.UpdateDeposit(uid, d)
	if errors.Is(err, errNotFound) {
		return fail(c, fiber.StatusNotFound, "Уплата не постоји")
	} else if err != nil {
		return internal(c, err)
	}
	return c.JSON(updated)
}

// DeleteDeposit: DELETE /api/deposits/:id
func (h *Handlers) DeleteDeposit(c fiber.Ctx) error {
	id, err := strconv.ParseInt(c.Params("id"), 10, 64)
	if err != nil {
		return fail(c, fiber.StatusBadRequest, "Неисправан ID")
	}
	if err := h.store.DeleteDeposit(currentUser(c).ID, id); errors.Is(err, errNotFound) {
		return fail(c, fiber.StatusNotFound, "Уплата не постоји")
	} else if err != nil {
		return internal(c, err)
	}
	return c.SendStatus(fiber.StatusNoContent)
}
