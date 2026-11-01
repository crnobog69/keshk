package main

import (
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
// Извозе се Кешкићи, трошкови и листе за куповину. Подешавања налога
// (лозинка, аватар, име за приказ) се НЕ извозе.
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

// ExportBackup чита све корисничке податке из базе.
func (s *Store) ExportBackup() (Backup, error) {
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
		FROM keshkici ORDER BY id`)
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
		FROM transactions ORDER BY id`)
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
		FROM shopping_lists ORDER BY id`)
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
	// created_at ставки није део модела ShoppingItem — допуни директно.
	rows, err = s.db.Query(`SELECT id, list_id, created_at FROM shopping_items`)
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

// ImportBackup у једној трансакцији ЗАМЕЊУЈЕ све Кешкиће, трошкове и листе садржајем копије.
// Ако нешто пође по злу, база остаје нетакнута.
func (s *Store) ImportBackup(b Backup) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for _, q := range []string{
		`DELETE FROM shopping_items`,
		`DELETE FROM shopping_lists`,
		`DELETE FROM transactions`,
		`DELETE FROM keshkici`,
	} {
		if _, err := tx.Exec(q); err != nil {
			return err
		}
	}

	orNow := func(v string) any {
		if strings.TrimSpace(v) == "" {
			return time.Now().UTC().Format("2006-01-02T15:04:05Z")
		}
		return v
	}

	for _, k := range b.Keshkici {
		def := 0
		if k.IsDefault {
			def = 1
		}
		if _, err := tx.Exec(`
			INSERT INTO keshkici (id, month, dropped_month, title, amount, currency, rate, amount_rsd, is_default, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			k.ID, k.Month, k.DroppedMonth, k.Title, round2(k.Amount), k.Currency, k.Rate, round2(k.Amount*k.Rate), def, orNow(k.CreatedAt)); err != nil {
			return err
		}
	}
	for _, t := range b.Transactions {
		if _, err := tx.Exec(`
			INSERT INTO transactions (id, keshkic_id, month, date, title, amount, currency, rate, amount_rsd, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			t.ID, t.KeshkicID, t.Date[:7], t.Date, t.Title, round2(t.Amount), t.Currency, t.Rate, round2(t.Amount*t.Rate), orNow(t.CreatedAt)); err != nil {
			return err
		}
	}
	for _, l := range b.Lists {
		if _, err := tx.Exec(`
			INSERT INTO shopping_lists (id, title, keshkic_id, currency, due_date, tx_id, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?)`,
			l.ID, l.Title, nullID(l.KeshkicID), l.Currency, l.DueDate, nullID(l.TxID), orNow(l.CreatedAt)); err != nil {
			return err
		}
		for _, it := range l.Items {
			checked := 0
			if it.Checked {
				checked = 1
			}
			if _, err := tx.Exec(`
				INSERT INTO shopping_items (id, list_id, name, price, checked, position, created_at)
				VALUES (?, ?, ?, ?, ?, ?, ?)`,
				it.ID, l.ID, it.Name, nullPrice(it.Price), checked, it.Position, orNow(it.CreatedAt)); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

// ---------------------------------------------------------------------
// HTTP
// ---------------------------------------------------------------------

// ExportData: GET /api/export — преузимање резервне копије као JSON датотеке.
func (h *Handlers) ExportData(c fiber.Ctx) error {
	b, err := h.store.ExportBackup()
	if err != nil {
		return internal(c, err)
	}
	out, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return internal(c, err)
	}
	c.Set("Content-Type", "application/json; charset=utf-8")
	c.Set("Content-Disposition", `attachment; filename="keshk-backup-`+time.Now().Format("2006-01-02")+`.json"`)
	return c.Send(out)
}

// ImportData: POST /api/import — замењује све податке садржајем резервне копије.
func (h *Handlers) ImportData(c fiber.Ctx) error {
	var b Backup
	if err := json.Unmarshal(c.Body(), &b); err != nil {
		return fail(c, fiber.StatusBadRequest, "Датотека није исправан JSON")
	}
	if err := b.normalize(); err != nil {
		return fail(c, fiber.StatusBadRequest, err.Error())
	}
	if err := h.store.ImportBackup(b); err != nil {
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
