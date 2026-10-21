package main

import (
	"database/sql"
	"errors"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gofiber/fiber/v3"
)

// ---------------------------------------------------------------------
// Модел: Листе за куповину
// ---------------------------------------------------------------------

// ShoppingItem је један производ на листи. Цена није обавезна.
type ShoppingItem struct {
	ID       int64    `json:"id"`
	ListID   int64    `json:"listId"`
	Name     string   `json:"name"`
	Price    *float64 `json:"price"` // nil = без цене
	Checked  bool     `json:"checked"`
	Position int      `json:"position"`
}

// ShoppingList је листа за куповину. DueDate == "" значи без рока (бесконачно).
type ShoppingList struct {
	ID           int64          `json:"id"`
	Title        string         `json:"title"`
	KeshkicID    int64          `json:"keshkicId"` // 0 = није изабран
	KeshkicTitle string         `json:"keshkicTitle"`
	Currency     string         `json:"currency"`
	DueDate      string         `json:"dueDate"` // YYYY-MM-DD или ""
	TxID         int64          `json:"txId"`    // трошак настао из листе (0 = није одузето)
	TxAmount     float64        `json:"txAmount"`
	TxDate       string         `json:"txDate"`
	TxKeshkicID  int64          `json:"txKeshkicId"`
	CreatedAt    string         `json:"createdAt"`
	ItemCount    int            `json:"itemCount"`
	CheckedCount int            `json:"checkedCount"`
	Total        float64        `json:"total"`    // збир цена штиклираних (купљених) ставки
	TotalAll     float64        `json:"totalAll"` // збир свих унетих цена (процена)
	Items        []ShoppingItem `json:"items,omitempty"`
}

// migrateExtra креира табеле за подешавања и листе за куповину.
func (s *Store) migrateExtra() error {
	_, err := s.db.Exec(`
	CREATE TABLE IF NOT EXISTS settings (
		key   TEXT PRIMARY KEY,
		value TEXT NOT NULL
	);
	CREATE TABLE IF NOT EXISTS shopping_lists (
		id          INTEGER PRIMARY KEY AUTOINCREMENT,
		title       TEXT    NOT NULL,
		keshkic_id  INTEGER REFERENCES keshkici(id) ON DELETE SET NULL,
		currency    TEXT    NOT NULL DEFAULT 'RSD',
		due_date    TEXT    NOT NULL DEFAULT '',
		tx_id       INTEGER REFERENCES transactions(id) ON DELETE SET NULL,
		created_at  TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
	);
	CREATE TABLE IF NOT EXISTS shopping_items (
		id          INTEGER PRIMARY KEY AUTOINCREMENT,
		list_id     INTEGER NOT NULL REFERENCES shopping_lists(id) ON DELETE CASCADE,
		name        TEXT    NOT NULL,
		price       REAL,
		checked     INTEGER NOT NULL DEFAULT 0,
		position    INTEGER NOT NULL DEFAULT 0,
		created_at  TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
	);
	CREATE INDEX IF NOT EXISTS idx_items_list ON shopping_items(list_id, position);
	`)
	return err
}

const listSelect = `
	SELECT l.id, l.title, COALESCE(l.keshkic_id, 0), COALESCE(k.title, ''), l.currency, l.due_date,
	       COALESCE(t.id, 0), COALESCE(t.amount, 0), COALESCE(t.date, ''), COALESCE(t.keshkic_id, 0), l.created_at,
	       (SELECT COUNT(*) FROM shopping_items i WHERE i.list_id = l.id),
	       (SELECT COUNT(*) FROM shopping_items i WHERE i.list_id = l.id AND i.checked = 1),
	       (SELECT COALESCE(SUM(i.price), 0) FROM shopping_items i WHERE i.list_id = l.id AND i.checked = 1),
	       (SELECT COALESCE(SUM(i.price), 0) FROM shopping_items i WHERE i.list_id = l.id)
	FROM shopping_lists l
	LEFT JOIN keshkici k ON k.id = l.keshkic_id
	LEFT JOIN transactions t ON t.id = l.tx_id`

func scanList(sc interface{ Scan(...any) error }) (ShoppingList, error) {
	var l ShoppingList
	err := sc.Scan(&l.ID, &l.Title, &l.KeshkicID, &l.KeshkicTitle, &l.Currency, &l.DueDate,
		&l.TxID, &l.TxAmount, &l.TxDate, &l.TxKeshkicID, &l.CreatedAt,
		&l.ItemCount, &l.CheckedCount, &l.Total, &l.TotalAll)
	l.Total = round2(l.Total)
	l.TotalAll = round2(l.TotalAll)
	return l, err
}

func nullID(id int64) any {
	if id <= 0 {
		return nil
	}
	return id
}

func nullPrice(p *float64) any {
	if p == nil {
		return nil
	}
	return *p
}

func (s *Store) ListShoppingLists() ([]ShoppingList, error) {
	rows, err := s.db.Query(listSelect + ` ORDER BY (t.id IS NOT NULL) ASC, (l.due_date = '') ASC, l.due_date ASC, l.id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ShoppingList{}
	for rows.Next() {
		l, err := scanList(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

func (s *Store) GetShoppingList(id int64) (ShoppingList, error) {
	l, err := scanList(s.db.QueryRow(listSelect+` WHERE l.id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return l, errNotFound
	}
	if err != nil {
		return l, err
	}
	l.Items, err = s.listItems(id)
	return l, err
}

func (s *Store) listItems(listID int64) ([]ShoppingItem, error) {
	rows, err := s.db.Query(`
		SELECT id, list_id, name, price, checked, position
		FROM shopping_items WHERE list_id = ?
		ORDER BY position ASC, id ASC`, listID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ShoppingItem{}
	for rows.Next() {
		var it ShoppingItem
		var price sql.NullFloat64
		var checked int
		if err := rows.Scan(&it.ID, &it.ListID, &it.Name, &price, &checked, &it.Position); err != nil {
			return nil, err
		}
		if price.Valid {
			p := price.Float64
			it.Price = &p
		}
		it.Checked = checked == 1
		out = append(out, it)
	}
	return out, rows.Err()
}

func (s *Store) CreateShoppingList(l ShoppingList) (ShoppingList, error) {
	res, err := s.db.Exec(`
		INSERT INTO shopping_lists (title, keshkic_id, currency, due_date) VALUES (?, ?, ?, ?)`,
		l.Title, nullID(l.KeshkicID), l.Currency, l.DueDate)
	if err != nil {
		return l, err
	}
	id, _ := res.LastInsertId()
	return s.GetShoppingList(id)
}

func (s *Store) UpdateShoppingList(l ShoppingList) (ShoppingList, error) {
	res, err := s.db.Exec(`
		UPDATE shopping_lists SET title = ?, keshkic_id = ?, currency = ?, due_date = ? WHERE id = ?`,
		l.Title, nullID(l.KeshkicID), l.Currency, l.DueDate, l.ID)
	if err != nil {
		return l, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return l, errNotFound
	}
	return s.GetShoppingList(l.ID)
}

func (s *Store) DeleteShoppingList(id int64) error {
	if _, err := s.db.Exec(`DELETE FROM shopping_items WHERE list_id = ?`, id); err != nil {
		return err
	}
	res, err := s.db.Exec(`DELETE FROM shopping_lists WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errNotFound
	}
	return nil
}

// SetListTx везује (или одвезује, txID = 0) трошак за листу.
func (s *Store) SetListTx(id, txID, keshkicID int64) error {
	_, err := s.db.Exec(`
		UPDATE shopping_lists SET tx_id = ?, keshkic_id = COALESCE(?, keshkic_id) WHERE id = ?`,
		nullID(txID), nullID(keshkicID), id)
	return err
}

func (s *Store) listExists(id int64) bool {
	var one int
	return s.db.QueryRow(`SELECT 1 FROM shopping_lists WHERE id = ?`, id).Scan(&one) == nil
}

func (s *Store) AddShoppingItem(listID int64, name string, price *float64) error {
	if !s.listExists(listID) {
		return errNotFound
	}
	_, err := s.db.Exec(`
		INSERT INTO shopping_items (list_id, name, price, position)
		VALUES (?, ?, ?, COALESCE((SELECT MAX(position) + 1 FROM shopping_items WHERE list_id = ?), 0))`,
		listID, name, nullPrice(price), listID)
	return err
}

func (s *Store) UpdateShoppingItem(it ShoppingItem) error {
	checked := 0
	if it.Checked {
		checked = 1
	}
	res, err := s.db.Exec(`
		UPDATE shopping_items SET name = ?, price = ?, checked = ? WHERE id = ? AND list_id = ?`,
		it.Name, nullPrice(it.Price), checked, it.ID, it.ListID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errNotFound
	}
	return nil
}

func (s *Store) DeleteShoppingItem(listID, itemID int64) error {
	res, err := s.db.Exec(`DELETE FROM shopping_items WHERE id = ? AND list_id = ?`, itemID, listID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errNotFound
	}
	return nil
}

// DefaultKeshkicID враћа подразумевани активни Кешкић (или први активни).
func (s *Store) DefaultKeshkicID() int64 {
	var id int64
	_ = s.db.QueryRow(`
		SELECT id FROM keshkici WHERE dropped_month = ''
		ORDER BY is_default DESC, id ASC LIMIT 1`).Scan(&id)
	return id
}

// ---------------------------------------------------------------------
// HTTP
// ---------------------------------------------------------------------

func paramID(c fiber.Ctx, name string) (int64, bool) {
	id, err := strconv.ParseInt(c.Params(name), 10, 64)
	return id, err == nil && id > 0
}

type listInput struct {
	Title     string `json:"title"`
	KeshkicID int64  `json:"keshkicId"`
	Currency  string `json:"currency"`
	DueDate   string `json:"dueDate"`
}

func (h *Handlers) parseList(c fiber.Ctx) (ShoppingList, error) {
	var in listInput
	if err := c.Bind().JSON(&in); err != nil {
		return ShoppingList{}, errors.New("Неисправан захтев")
	}
	in.Title = strings.TrimSpace(in.Title)
	if in.Title == "" {
		return ShoppingList{}, errors.New("Унеси назив листе")
	}
	if utf8.RuneCountInString(in.Title) > 80 {
		return ShoppingList{}, errors.New("Назив листе је предугачак")
	}
	in.Currency = strings.ToUpper(strings.TrimSpace(in.Currency))
	if in.Currency == "" {
		in.Currency = "RSD"
	}
	if !isSupportedCurrency(in.Currency) {
		return ShoppingList{}, errors.New("Неподржана валута")
	}
	in.DueDate = strings.TrimSpace(in.DueDate)
	if in.DueDate != "" {
		if _, err := time.Parse("2006-01-02", in.DueDate); err != nil {
			return ShoppingList{}, errors.New("Неисправан датум рока")
		}
	}
	if in.KeshkicID > 0 {
		if _, err := h.store.GetKeshkic(in.KeshkicID); err != nil {
			return ShoppingList{}, errors.New("Изабрани Кешкић не постоји")
		}
	}
	return ShoppingList{Title: in.Title, KeshkicID: in.KeshkicID, Currency: in.Currency, DueDate: in.DueDate}, nil
}

func (h *Handlers) sendList(c fiber.Ctx, id int64, status int) error {
	l, err := h.store.GetShoppingList(id)
	if errors.Is(err, errNotFound) {
		return fail(c, fiber.StatusNotFound, "Листа не постоји")
	} else if err != nil {
		return internal(c, err)
	}
	return c.Status(status).JSON(l)
}

func (h *Handlers) ListLists(c fiber.Ctx) error {
	lists, err := h.store.ListShoppingLists()
	if err != nil {
		return internal(c, err)
	}
	return c.JSON(lists)
}

func (h *Handlers) GetList(c fiber.Ctx) error {
	id, ok := paramID(c, "id")
	if !ok {
		return fail(c, fiber.StatusBadRequest, "Неисправан ID")
	}
	return h.sendList(c, id, fiber.StatusOK)
}

func (h *Handlers) CreateList(c fiber.Ctx) error {
	l, err := h.parseList(c)
	if err != nil {
		return fail(c, fiber.StatusBadRequest, err.Error())
	}
	created, err := h.store.CreateShoppingList(l)
	if err != nil {
		return internal(c, err)
	}
	return c.Status(fiber.StatusCreated).JSON(created)
}

func (h *Handlers) UpdateList(c fiber.Ctx) error {
	id, ok := paramID(c, "id")
	if !ok {
		return fail(c, fiber.StatusBadRequest, "Неисправан ID")
	}
	l, err := h.parseList(c)
	if err != nil {
		return fail(c, fiber.StatusBadRequest, err.Error())
	}
	l.ID = id
	updated, err := h.store.UpdateShoppingList(l)
	if errors.Is(err, errNotFound) {
		return fail(c, fiber.StatusNotFound, "Листа не постоји")
	} else if err != nil {
		return internal(c, err)
	}
	return c.JSON(updated)
}

func (h *Handlers) DeleteList(c fiber.Ctx) error {
	id, ok := paramID(c, "id")
	if !ok {
		return fail(c, fiber.StatusBadRequest, "Неисправан ID")
	}
	if err := h.store.DeleteShoppingList(id); errors.Is(err, errNotFound) {
		return fail(c, fiber.StatusNotFound, "Листа не постоји")
	} else if err != nil {
		return internal(c, err)
	}
	return c.SendStatus(fiber.StatusNoContent)
}

type itemInput struct {
	Name    string   `json:"name"`
	Price   *float64 `json:"price"`
	Checked bool     `json:"checked"`
}

func parseItem(c fiber.Ctx) (itemInput, error) {
	var in itemInput
	if err := c.Bind().JSON(&in); err != nil {
		return in, errors.New("Неисправан захтев")
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		return in, errors.New("Унеси назив производа")
	}
	if utf8.RuneCountInString(in.Name) > 120 {
		return in, errors.New("Назив производа је предугачак")
	}
	if in.Price != nil {
		p := *in.Price
		if p < 0 || p > 1e10 {
			return in, errors.New("Неисправна цена")
		}
		if p == 0 {
			in.Price = nil
		} else {
			p = round2(p)
			in.Price = &p
		}
	}
	return in, nil
}

func (h *Handlers) AddItem(c fiber.Ctx) error {
	id, ok := paramID(c, "id")
	if !ok {
		return fail(c, fiber.StatusBadRequest, "Неисправан ID")
	}
	in, err := parseItem(c)
	if err != nil {
		return fail(c, fiber.StatusBadRequest, err.Error())
	}
	if err := h.store.AddShoppingItem(id, in.Name, in.Price); errors.Is(err, errNotFound) {
		return fail(c, fiber.StatusNotFound, "Листа не постоји")
	} else if err != nil {
		return internal(c, err)
	}
	return h.sendList(c, id, fiber.StatusCreated)
}

func (h *Handlers) UpdateItem(c fiber.Ctx) error {
	id, ok1 := paramID(c, "id")
	itemID, ok2 := paramID(c, "itemId")
	if !ok1 || !ok2 {
		return fail(c, fiber.StatusBadRequest, "Неисправан ID")
	}
	in, err := parseItem(c)
	if err != nil {
		return fail(c, fiber.StatusBadRequest, err.Error())
	}
	err = h.store.UpdateShoppingItem(ShoppingItem{ID: itemID, ListID: id, Name: in.Name, Price: in.Price, Checked: in.Checked})
	if errors.Is(err, errNotFound) {
		return fail(c, fiber.StatusNotFound, "Ставка не постоји")
	} else if err != nil {
		return internal(c, err)
	}
	return h.sendList(c, id, fiber.StatusOK)
}

func (h *Handlers) DeleteItem(c fiber.Ctx) error {
	id, ok1 := paramID(c, "id")
	itemID, ok2 := paramID(c, "itemId")
	if !ok1 || !ok2 {
		return fail(c, fiber.StatusBadRequest, "Неисправан ID")
	}
	if err := h.store.DeleteShoppingItem(id, itemID); errors.Is(err, errNotFound) {
		return fail(c, fiber.StatusNotFound, "Ставка не постоји")
	} else if err != nil {
		return internal(c, err)
	}
	return h.sendList(c, id, fiber.StatusOK)
}

// ChargeList одузима збир купљених ставки од Кешкића (прави или ажурира трошак).
func (h *Handlers) ChargeList(c fiber.Ctx) error {
	id, ok := paramID(c, "id")
	if !ok {
		return fail(c, fiber.StatusBadRequest, "Неисправан ID")
	}
	var body struct {
		KeshkicID int64 `json:"keshkicId"`
	}
	if len(c.Body()) > 0 {
		if err := c.Bind().JSON(&body); err != nil {
			return fail(c, fiber.StatusBadRequest, "Неисправан захтев")
		}
	}

	l, err := h.store.GetShoppingList(id)
	if errors.Is(err, errNotFound) {
		return fail(c, fiber.StatusNotFound, "Листа не постоји")
	} else if err != nil {
		return internal(c, err)
	}
	if !(l.Total > 0) {
		return fail(c, fiber.StatusBadRequest, "Штиклирај купљене ставке које имају цену")
	}

	kid := body.KeshkicID
	if kid <= 0 {
		kid = l.KeshkicID
	}
	if kid <= 0 {
		kid = h.store.DefaultKeshkicID()
	}
	if kid <= 0 {
		return fail(c, fiber.StatusBadRequest, "Прво додај бар један Кешкић")
	}
	if _, err := h.store.GetKeshkic(kid); err != nil {
		return fail(c, fiber.StatusBadRequest, "Изабрани Кешкић не постоји")
	}

	rate := 1.0
	if l.Currency != "RSD" {
		rate = h.rates.Rate(l.Currency)
	}
	tx := Tx{
		KeshkicID: kid,
		Title:     l.Title,
		Amount:    l.Total,
		Currency:  l.Currency,
		Rate:      rate,
		AmountRSD: round2(l.Total * rate),
	}

	if l.TxID > 0 {
		if old, err := h.store.GetTx(l.TxID); err == nil {
			tx.ID = old.ID
			tx.Date = old.Date
			if _, err := h.store.UpdateTx(tx); err != nil {
				return internal(c, err)
			}
		}
	}
	if tx.ID == 0 {
		tx.Date = time.Now().Format("2006-01-02")
		created, err := h.store.CreateTx(tx)
		if err != nil {
			return internal(c, err)
		}
		tx.ID = created.ID
	}

	if err := h.store.SetListTx(id, tx.ID, kid); err != nil {
		return internal(c, err)
	}
	return h.sendList(c, id, fiber.StatusOK)
}

// UnchargeList брише трошак настао из листе (новац се враћа Кешкићу).
func (h *Handlers) UnchargeList(c fiber.Ctx) error {
	id, ok := paramID(c, "id")
	if !ok {
		return fail(c, fiber.StatusBadRequest, "Неисправан ID")
	}
	l, err := h.store.GetShoppingList(id)
	if errors.Is(err, errNotFound) {
		return fail(c, fiber.StatusNotFound, "Листа не постоји")
	} else if err != nil {
		return internal(c, err)
	}
	if l.TxID > 0 {
		if err := h.store.DeleteTx(l.TxID); err != nil && !errors.Is(err, errNotFound) {
			return internal(c, err)
		}
	}
	if err := h.store.SetListTx(id, 0, 0); err != nil {
		return internal(c, err)
	}
	return h.sendList(c, id, fiber.StatusOK)
}
