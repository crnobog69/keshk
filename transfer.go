package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gofiber/fiber/v3"
)

// ---------------------------------------------------------------------
// Извоз / увоз података (резервна копија у JSON формату)
//
// Извозе се Кешкићи, трошкови и листе за куповину тренутно пријављеног корисника.
// Подешавања налога (лозинка, аватар, име за приказ) и подаци других налога се НЕ дирају.
// ---------------------------------------------------------------------

const (
	backupApp     = "keshk"
	backupVersion = 1
)

type backupKeshkic struct {
	ID           int64   `json:"id"`
	Month        string  `json:"month"`
	DroppedMonth string  `json:"droppedMonth"`
	Title        string  `json:"title"`
	Amount       float64 `json:"amount"`
	Currency     string  `json:"currency"`
	Rate         float64 `json:"rate"`
	IsDefault    bool    `json:"isDefault"`
	CreatedAt    string  `json:"createdAt"`
}

type backupTx struct {
	ID        int64   `json:"id"`
	KeshkicID int64   `json:"keshkicId"`
	Date      string  `json:"date"`
	Title     string  `json:"title"`
	Amount    float64 `json:"amount"`
	Currency  string  `json:"currency"`
	Rate      float64 `json:"rate"`
	CreatedAt string  `json:"createdAt"`
}

type backupItem struct {
	ID        int64    `json:"id"`
	Name      string   `json:"name"`
	Price     *float64 `json:"price"`
	Checked   bool     `json:"checked"`
	Position  int      `json:"position"`
	CreatedAt string   `json:"createdAt"`
}

type backupList struct {
	ID        int64        `json:"id"`
	Title     string       `json:"title"`
	KeshkicID int64        `json:"keshkicId"` // 0 = није изабран
	Currency  string       `json:"currency"`
	DueDate   string       `json:"dueDate"`
	TxID      int64        `json:"txId"` // 0 = није одузето
	CreatedAt string       `json:"createdAt"`
	Items     []backupItem `json:"items"`
}

type Backup struct {
	App          string          `json:"app"`
	Version      int             `json:"version"`
	ExportedAt   string          `json:"exportedAt"`
	Keshkici     []backupKeshkic `json:"keshkici"`
	Transactions []backupTx      `json:"transactions"`
	Lists        []backupList    `json:"lists"`
}

// ExportBackup чита све податке датог корисника из базе.
func (s *Store) ExportBackup(userID int64) (Backup, error) {
	b := Backup{
		App:          backupApp,
		Version:      backupVersion,
		ExportedAt:   time.Now().UTC().Format(time.RFC3339),
		Keshkici:     []backupKeshkic{},
		Transactions: []backupTx{},
		Lists:        []backupList{},
	}

	rows, err := s.db.Query(`
		SELECT id, month, dropped_month, title, amount, currency, rate, is_default, created_at
		FROM keshkici WHERE user_id = $1 ORDER BY id`, userID)
	if err != nil {
		return b, err
	}
	for rows.Next() {
		var k backupKeshkic
		var def int
		if err := rows.Scan(&k.ID, &k.Month, &k.DroppedMonth, &k.Title, &k.Amount, &k.Currency, &k.Rate, &def, &k.CreatedAt); err != nil {
			rows.Close()
			return b, err
		}
		k.IsDefault = def == 1
		b.Keshkici = append(b.Keshkici, k)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return b, err
	}
	rows.Close()

	rows, err = s.db.Query(`
		SELECT id, keshkic_id, date, title, amount, currency, rate, created_at
		FROM transactions WHERE user_id = $1 ORDER BY id`, userID)
	if err != nil {
		return b, err
	}
	for rows.Next() {
		var t backupTx
		if err := rows.Scan(&t.ID, &t.KeshkicID, &t.Date, &t.Title, &t.Amount, &t.Currency, &t.Rate, &t.CreatedAt); err != nil {
			rows.Close()
			return b, err
		}
		b.Transactions = append(b.Transactions, t)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return b, err
	}
	rows.Close()

	rows, err = s.db.Query(`
		SELECT id, title, COALESCE(keshkic_id, 0), currency, due_date, COALESCE(tx_id, 0), created_at
		FROM shopping_lists WHERE user_id = $1 ORDER BY id`, userID)
	if err != nil {
		return b, err
	}
	for rows.Next() {
		var l backupList
		if err := rows.Scan(&l.ID, &l.Title, &l.KeshkicID, &l.Currency, &l.DueDate, &l.TxID, &l.CreatedAt); err != nil {
			rows.Close()
			return b, err
		}
		l.Items = []backupItem{}
		b.Lists = append(b.Lists, l)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return b, err
	}
	rows.Close()

	for i := range b.Lists {
		items, err := s.listItems(b.Lists[i].ID)
		if err != nil {
			return b, err
		}
		for _, it := range items {
			b.Lists[i].Items = append(b.Lists[i].Items, backupItem{
				ID: it.ID, Name: it.Name, Price: it.Price, Checked: it.Checked, Position: it.Position,
			})
		}
	}
	// created_at ставки није део модела ShoppingItem — допуни директно за листе овог корисника.
	rows, err = s.db.Query(`
		SELECT si.id, si.list_id, si.created_at
		FROM shopping_items si
		JOIN shopping_lists sl ON sl.id = si.list_id
		WHERE sl.user_id = $1`, userID)
	if err != nil {
		return b, err
	}
	created := map[int64]string{}
	for rows.Next() {
		var id, listID int64
		var at string
		if err := rows.Scan(&id, &listID, &at); err != nil {
			rows.Close()
			return b, err
		}
		created[id] = at
	}
	rows.Close()
	for i := range b.Lists {
		for j := range b.Lists[i].Items {
			b.Lists[i].Items[j].CreatedAt = created[b.Lists[i].Items[j].ID]
		}
	}
	return b, nil
}

// normalize проверава и чисти садржај резервне копије. Враћа грешку на корисничком језику.
func (b *Backup) normalize() error {
	if b.App != backupApp {
		return errors.New("Ово није Keshk резервна копија")
	}
	if b.Version < 1 || b.Version > backupVersion {
		return fmt.Errorf("Неподржана верзија резервне копије (%d)", b.Version)
	}

	kIDs := map[int64]bool{}
	for i := range b.Keshkici {
		k := &b.Keshkici[i]
		if k.ID <= 0 || kIDs[k.ID] {
			return errors.New("Неисправни ИД-јеви Кешкића у датотеци")
		}
		kIDs[k.ID] = true
		if !validMonth(k.Month) {
			return fmt.Errorf("Кешкић #%d: неисправан месец", k.ID)
		}
		if k.DroppedMonth != "" && !validMonth(k.DroppedMonth) {
			return fmt.Errorf("Кешкић #%d: неисправан месец одбацивања", k.ID)
		}
		k.Title = strings.TrimSpace(k.Title)
		if k.Title == "" {
			k.Title = "Кешкић"
		}
		if utf8.RuneCountInString(k.Title) > 60 {
			return fmt.Errorf("Кешкић #%d: назив је предугачак", k.ID)
		}
		if !(k.Amount > 0) || k.Amount > 1e10 {
			return fmt.Errorf("Кешкић #%d: неисправан износ", k.ID)
		}
		k.Currency = strings.ToUpper(strings.TrimSpace(k.Currency))
		if !isSupportedCurrency(k.Currency) {
			return fmt.Errorf("Кешкић #%d: неподржана валута", k.ID)
		}
		if k.Currency == "RSD" {
			k.Rate = 1
		} else if !(k.Rate > 0) {
			return fmt.Errorf("Кешкић #%d: неисправан курс", k.ID)
		}
	}

	// Највише један подразумевани међу активним; ако га нема, први активни постаје подразумевани.
	hasDefault, firstActive := false, -1
	for i := range b.Keshkici {
		k := &b.Keshkici[i]
		if k.DroppedMonth != "" {
			k.IsDefault = false
			continue
		}
		if firstActive < 0 {
			firstActive = i
		}
		if k.IsDefault {
			if hasDefault {
				k.IsDefault = false
			}
			hasDefault = true
		}
	}
	if !hasDefault && firstActive >= 0 {
		b.Keshkici[firstActive].IsDefault = true
	}

	txIDs := map[int64]bool{}
	for i := range b.Transactions {
		t := &b.Transactions[i]
		if t.ID <= 0 || txIDs[t.ID] {
			return errors.New("Неисправни ИД-јеви трошкова у датотеци")
		}
		txIDs[t.ID] = true
		if !kIDs[t.KeshkicID] {
			return fmt.Errorf("Трошак #%d упућује на непостојећи Кешкић", t.ID)
		}
		if _, err := time.Parse("2006-01-02", t.Date); err != nil {
			return fmt.Errorf("Трошак #%d: неисправан датум", t.ID)
		}
		t.Title = strings.TrimSpace(t.Title)
		if utf8.RuneCountInString(t.Title) > 120 {
			return fmt.Errorf("Трошак #%d: опис је предугачак", t.ID)
		}
		if !(t.Amount > 0) || t.Amount > 1e10 {
			return fmt.Errorf("Трошак #%d: неисправан износ", t.ID)
		}
		t.Currency = strings.ToUpper(strings.TrimSpace(t.Currency))
		if !isSupportedCurrency(t.Currency) {
			return fmt.Errorf("Трошак #%d: неподржана валута", t.ID)
		}
		if t.Currency == "RSD" {
			t.Rate = 1
		} else if !(t.Rate > 0) {
			return fmt.Errorf("Трошак #%d: неисправан курс", t.ID)
		}
	}

	lIDs := map[int64]bool{}
	itemIDs := map[int64]bool{}
	for i := range b.Lists {
		l := &b.Lists[i]
		if l.ID <= 0 || lIDs[l.ID] {
			return errors.New("Неисправни ИД-јеви листа у датотеци")
		}
		lIDs[l.ID] = true
		l.Title = strings.TrimSpace(l.Title)
		if l.Title == "" || utf8.RuneCountInString(l.Title) > 80 {
			return fmt.Errorf("Листа #%d: неисправан назив", l.ID)
		}
		l.Currency = strings.ToUpper(strings.TrimSpace(l.Currency))
		if l.Currency == "" {
			l.Currency = "RSD"
		}
		if !isSupportedCurrency(l.Currency) {
			return fmt.Errorf("Листа #%d: неподржана валута", l.ID)
		}
		if l.DueDate != "" {
			if _, err := time.Parse("2006-01-02", l.DueDate); err != nil {
				return fmt.Errorf("Листа #%d: неисправан рок", l.ID)
			}
		}
		if !kIDs[l.KeshkicID] {
			l.KeshkicID = 0
		}
		if !txIDs[l.TxID] {
			l.TxID = 0
		}
		for j := range l.Items {
			it := &l.Items[j]
			if it.ID <= 0 || itemIDs[it.ID] {
				return errors.New("Неисправни ИД-јеви ставки у датотеци")
			}
			itemIDs[it.ID] = true
			it.Name = strings.TrimSpace(it.Name)
			if it.Name == "" || utf8.RuneCountInString(it.Name) > 120 {
				return fmt.Errorf("Листа #%d: неисправан назив ставке", l.ID)
			}
			if it.Price != nil {
				p := *it.Price
				if p < 0 || p > 1e10 {
					return fmt.Errorf("Листа #%d, ставка „%s”: неисправна цена", l.ID, it.Name)
				}
				if p == 0 {
					it.Price = nil
				} else {
					p = round2(p)
					it.Price = &p
				}
			}
		}
	}
	return nil
}

func rowIDTaken(tx *sql.Tx, table string, id int64) (bool, error) {
	var one int
	err := tx.QueryRow(`SELECT 1 FROM `+table+` WHERE id = $1`, id).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

func syncIdentitySequences(tx *sql.Tx) error {
	for _, tbl := range []string{"keshkici", "transactions", "shopping_lists", "shopping_items"} {
		q := fmt.Sprintf(
			`SELECT setval(pg_get_serial_sequence('%s', 'id'), COALESCE((SELECT MAX(id) FROM %s), 1), EXISTS (SELECT 1 FROM %s))`,
			tbl, tbl, tbl,
		)
		if _, err := tx.Exec(q); err != nil {
			return err
		}
	}
	return nil
}

func backupHasIDCollision(tx *sql.Tx, b Backup) (bool, error) {
	for _, k := range b.Keshkici {
		if taken, err := rowIDTaken(tx, "keshkici", k.ID); err != nil || taken {
			return taken, err
		}
	}
	for _, t := range b.Transactions {
		if taken, err := rowIDTaken(tx, "transactions", t.ID); err != nil || taken {
			return taken, err
		}
	}
	for _, l := range b.Lists {
		if taken, err := rowIDTaken(tx, "shopping_lists", l.ID); err != nil || taken {
			return taken, err
		}
		for _, it := range l.Items {
			if taken, err := rowIDTaken(tx, "shopping_items", it.ID); err != nil || taken {
				return taken, err
			}
		}
	}
	return false, nil
}

// ImportBackup у једној трансакцији ЗАМЕЊУЈЕ све Кешкиће, трошкове и листе само за датог корисника.
// Очувава ИД-јеве из резервне копије када су сви слободни, а аутоматски генерише нове и премапира везе
// ако неки ИД већ припада другом налогу (без обзира на редослед записа у JSON датотеци).
func (s *Store) ImportBackup(userID int64, b Backup) error {
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
	} {
		if _, err := tx.Exec(q, userID); err != nil {
			return err
		}
	}

	if err := syncIdentitySequences(tx); err != nil {
		return err
	}

	remap, err := backupHasIDCollision(tx, b)
	if err != nil {
		return err
	}

	orNow := func(v string) any {
		if strings.TrimSpace(v) == "" {
			return time.Now().UTC().Format("2006-01-02T15:04:05Z")
		}
		return v
	}

	kMap := make(map[int64]int64, len(b.Keshkici))
	for _, k := range b.Keshkici {
		def := 0
		if k.IsDefault {
			def = 1
		}
		if !remap {
			if _, err := tx.Exec(`
				INSERT INTO keshkici (id, user_id, month, dropped_month, title, amount, currency, rate, amount_rsd, is_default, created_at)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
				k.ID, userID, k.Month, k.DroppedMonth, k.Title, round2(k.Amount), k.Currency, k.Rate, round2(k.Amount*k.Rate), def, orNow(k.CreatedAt)); err != nil {
				return err
			}
			kMap[k.ID] = k.ID
		} else {
			var newID int64
			if err := tx.QueryRow(`
				INSERT INTO keshkici (user_id, month, dropped_month, title, amount, currency, rate, amount_rsd, is_default, created_at)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
				RETURNING id`,
				userID, k.Month, k.DroppedMonth, k.Title, round2(k.Amount), k.Currency, k.Rate, round2(k.Amount*k.Rate), def, orNow(k.CreatedAt)).Scan(&newID); err != nil {
				return err
			}
			kMap[k.ID] = newID
		}
	}

	txMap := make(map[int64]int64, len(b.Transactions))
	for _, t := range b.Transactions {
		mappedKID := kMap[t.KeshkicID]
		if !remap {
			if _, err := tx.Exec(`
				INSERT INTO transactions (id, user_id, keshkic_id, month, date, title, amount, currency, rate, amount_rsd, created_at)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
				t.ID, userID, mappedKID, t.Date[:7], t.Date, t.Title, round2(t.Amount), t.Currency, t.Rate, round2(t.Amount*t.Rate), orNow(t.CreatedAt)); err != nil {
				return err
			}
			txMap[t.ID] = t.ID
		} else {
			var newID int64
			if err := tx.QueryRow(`
				INSERT INTO transactions (user_id, keshkic_id, month, date, title, amount, currency, rate, amount_rsd, created_at)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
				RETURNING id`,
				userID, mappedKID, t.Date[:7], t.Date, t.Title, round2(t.Amount), t.Currency, t.Rate, round2(t.Amount*t.Rate), orNow(t.CreatedAt)).Scan(&newID); err != nil {
				return err
			}
			txMap[t.ID] = newID
		}
	}

	for _, l := range b.Lists {
		mappedKID := kMap[l.KeshkicID]
		mappedTxID := txMap[l.TxID]
		actualListID := l.ID
		if !remap {
			if _, err := tx.Exec(`
				INSERT INTO shopping_lists (id, user_id, title, keshkic_id, currency, due_date, tx_id, created_at)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
				l.ID, userID, l.Title, nullID(mappedKID), l.Currency, l.DueDate, nullID(mappedTxID), orNow(l.CreatedAt)); err != nil {
				return err
			}
		} else {
			if err := tx.QueryRow(`
				INSERT INTO shopping_lists (user_id, title, keshkic_id, currency, due_date, tx_id, created_at)
				VALUES ($1, $2, $3, $4, $5, $6, $7)
				RETURNING id`,
				userID, l.Title, nullID(mappedKID), l.Currency, l.DueDate, nullID(mappedTxID), orNow(l.CreatedAt)).Scan(&actualListID); err != nil {
				return err
			}
		}
		for _, it := range l.Items {
			checked := 0
			if it.Checked {
				checked = 1
			}
			if !remap {
				if _, err := tx.Exec(`
					INSERT INTO shopping_items (id, list_id, name, price, checked, position, created_at)
					VALUES ($1, $2, $3, $4, $5, $6, $7)`,
					it.ID, actualListID, it.Name, nullPrice(it.Price), checked, it.Position, orNow(it.CreatedAt)); err != nil {
					return err
				}
			} else {
				if _, err := tx.Exec(`
					INSERT INTO shopping_items (list_id, name, price, checked, position, created_at)
					VALUES ($1, $2, $3, $4, $5, $6)`,
					actualListID, it.Name, nullPrice(it.Price), checked, it.Position, orNow(it.CreatedAt)); err != nil {
					return err
				}
			}
		}
	}

	if err := syncIdentitySequences(tx); err != nil {
		return err
	}
	return tx.Commit()
}

// ---------------------------------------------------------------------
// HTTP
// ---------------------------------------------------------------------

// ExportData: GET /api/export — преузимање резервне копије као JSON датотеке.
func (h *Handlers) ExportData(c fiber.Ctx) error {
	u := currentUser(c)
	b, err := h.store.ExportBackup(u.ID)
	if err != nil {
		return internal(c, err)
	}
	out, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return internal(c, err)
	}
	c.Set("Content-Type", "application/json; charset=utf-8")
	c.Set("Content-Disposition", `attachment; filename="keshk-`+u.Username+`-`+time.Now().Format("2006-01-02")+`.json"`)
	return c.Send(out)
}

// ImportData: POST /api/import — замењује све податке тренутног корисника садржајем резервне копије.
func (h *Handlers) ImportData(c fiber.Ctx) error {
	var b Backup
	if err := json.Unmarshal(c.Body(), &b); err != nil {
		return fail(c, fiber.StatusBadRequest, "Датотека није исправан JSON")
	}
	if err := b.normalize(); err != nil {
		return fail(c, fiber.StatusBadRequest, err.Error())
	}
	if err := h.store.ImportBackup(currentUser(c).ID, b); err != nil {
		return internal(c, err)
	}
	items := 0
	for _, l := range b.Lists {
		items += len(l.Items)
	}
	return c.JSON(fiber.Map{
		"ok":           true,
		"keshkici":     len(b.Keshkici),
		"transactions": len(b.Transactions),
		"lists":        len(b.Lists),
		"items":        items,
	})
}
