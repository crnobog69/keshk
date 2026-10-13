package main

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

// Keshkic is a budget stash/envelope. It starts in Month and lives on in every
// following month until it is dropped (DroppedMonth = last month it is shown).
// Every Keshkic is identified by ID only - titles may repeat.
type Keshkic struct {
	ID           int64   `json:"id"`
	Month        string  `json:"month"`        // YYYY-MM (start month)
	DroppedMonth string  `json:"droppedMonth"` // YYYY-MM or "" while active
	Dropped      bool    `json:"dropped"`      // dropped as of the viewed month
	Title        string  `json:"title"`
	Amount       float64 `json:"amount"`
	Currency     string  `json:"currency"`
	Rate         float64 `json:"rate"` // RSD per 1 unit of Currency
	AmountRSD    float64 `json:"amountRsd"`
	IsDefault    bool    `json:"isDefault"`
	SpentRSD     float64 `json:"spentRsd"` // total expenses deducted up to the viewed month
	Balance      float64 `json:"balance"`  // AmountRSD - SpentRSD
	TxCount      int     `json:"txCount"`  // number of expenses up to the viewed month
	CreatedAt    string  `json:"createdAt"`
}

// Tx is an expense transaction allocated to a specific Keshkic.
type Tx struct {
	ID           int64   `json:"id"`
	KeshkicID    int64   `json:"keshkicId"`
	KeshkicTitle string  `json:"keshkicTitle,omitempty"`
	Month        string  `json:"month"` // YYYY-MM
	Date         string  `json:"date"`  // YYYY-MM-DD
	Title        string  `json:"title"`
	Amount       float64 `json:"amount"`
	Currency     string  `json:"currency"`
	Rate         float64 `json:"rate"`
	AmountRSD    float64 `json:"amountRsd"`
	CreatedAt    string  `json:"createdAt"`
}

type Store struct{ db *sql.DB }

var errNotFound = errors.New("not found")

func openStore(path string) (*Store, error) {
	if dir := filepath.Dir(path); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)

	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("миграција базе: %w", err)
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) hasColumn(table, col string) bool {
	rows, err := s.db.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		return false
	}
	defer rows.Close()
	for rows.Next() {
		var cid, notnull, pk int
		var name, ctype string
		var dflt any
		if rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk) == nil && name == col {
			return true
		}
	}
	return false
}

func (s *Store) migrate() error {
	hasKindCol := s.hasColumn("transactions", "kind")

	_, err := s.db.Exec(`
	CREATE TABLE IF NOT EXISTS keshkici (
		id            INTEGER PRIMARY KEY AUTOINCREMENT,
		month         TEXT    NOT NULL,
		dropped_month TEXT    NOT NULL DEFAULT '',
		title         TEXT    NOT NULL,
		amount        REAL    NOT NULL,
		currency      TEXT    NOT NULL DEFAULT 'RSD',
		rate          REAL    NOT NULL DEFAULT 1,
		amount_rsd    REAL    NOT NULL,
		is_default    INTEGER NOT NULL DEFAULT 0,
		created_at    TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
	);
	CREATE INDEX IF NOT EXISTS idx_keshkici_month ON keshkici(month);
	`)
	if err != nil {
		return err
	}

	// Старије базе без dropped_month
	if !s.hasColumn("keshkici", "dropped_month") {
		if _, err := s.db.Exec(`ALTER TABLE keshkici ADD COLUMN dropped_month TEXT NOT NULL DEFAULT ''`); err != nil {
			return err
		}
	}

	// Миграција са најстаријег модела (income/expense у једној табели)
	if hasKindCol {
		_, _ = s.db.Exec(`
			INSERT INTO keshkici (month, title, amount, currency, rate, amount_rsd, is_default, created_at)
			SELECT month, CASE WHEN title = '' THEN 'Кешкић' ELSE title END, amount, currency, rate, amount_rsd, 0, created_at
			FROM transactions WHERE kind = 'income'
		`)
		_, _ = s.db.Exec(`
			INSERT INTO keshkici (month, title, amount, currency, rate, amount_rsd, is_default)
			SELECT DISTINCT month, 'Мама', 5000, 'RSD', 1, 5000, 1
			FROM transactions
			WHERE month NOT IN (SELECT DISTINCT month FROM keshkici)
		`)
		_, _ = s.db.Exec(`UPDATE keshkici SET is_default = 0`)
		_, _ = s.db.Exec(`UPDATE keshkici SET is_default = 1 WHERE id = (SELECT MIN(id) FROM keshkici)`)

		_, err = s.db.Exec(`
		CREATE TABLE IF NOT EXISTS transactions_new (
			id          INTEGER PRIMARY KEY AUTOINCREMENT,
			keshkic_id  INTEGER NOT NULL REFERENCES keshkici(id) ON DELETE CASCADE,
			month       TEXT    NOT NULL,
			date        TEXT    NOT NULL,
			title       TEXT    NOT NULL DEFAULT '',
			amount      REAL    NOT NULL,
			currency    TEXT    NOT NULL DEFAULT 'RSD',
			rate        REAL    NOT NULL DEFAULT 1,
			amount_rsd  REAL    NOT NULL,
			created_at  TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
		);
		`)
		if err != nil {
			return err
		}
		_, _ = s.db.Exec(`
			INSERT INTO transactions_new (id, keshkic_id, month, date, title, amount, currency, rate, amount_rsd, created_at)
			SELECT t.id,
			       COALESCE((SELECT k.id FROM keshkici k WHERE k.month = t.month ORDER BY k.id LIMIT 1), 1),
			       t.month, t.date, t.title, t.amount, t.currency, t.rate, t.amount_rsd, t.created_at
			FROM transactions t
			WHERE t.kind = 'expense'
		`)
		_, _ = s.db.Exec(`DROP TABLE transactions; ALTER TABLE transactions_new RENAME TO transactions;`)
	}

	_, err = s.db.Exec(`
	CREATE TABLE IF NOT EXISTS transactions (
		id          INTEGER PRIMARY KEY AUTOINCREMENT,
		keshkic_id  INTEGER NOT NULL REFERENCES keshkici(id) ON DELETE CASCADE,
		month       TEXT    NOT NULL,
		date        TEXT    NOT NULL,
		title       TEXT    NOT NULL DEFAULT '',
		amount      REAL    NOT NULL,
		currency    TEXT    NOT NULL DEFAULT 'RSD',
		rate        REAL    NOT NULL DEFAULT 1,
		amount_rsd  REAL    NOT NULL,
		created_at  TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
	);
	CREATE INDEX IF NOT EXISTS idx_tx_month ON transactions(month, date);
	CREATE INDEX IF NOT EXISTS idx_tx_keshkic ON transactions(keshkic_id);
	`)
	return err
}

// ---------------------------------------------------------------------
// Keshkici
// ---------------------------------------------------------------------

// Кешкић је видљив у месецу M ако је почео <= M и није одбачен пре M.
const keshkicVisible = `k.month <= ? AND (k.dropped_month = '' OR k.dropped_month >= ?)`

func scanKeshkic(sc interface{ Scan(...any) error }, viewMonth string) (Keshkic, error) {
	var k Keshkic
	var isDef int
	err := sc.Scan(&k.ID, &k.Month, &k.DroppedMonth, &k.Title, &k.Amount, &k.Currency, &k.Rate,
		&k.AmountRSD, &isDef, &k.CreatedAt, &k.SpentRSD, &k.TxCount)
	if err != nil {
		return k, err
	}
	k.IsDefault = isDef == 1
	k.Dropped = k.DroppedMonth != "" && viewMonth >= k.DroppedMonth
	k.Balance = k.AmountRSD - k.SpentRSD
	return k, nil
}

// ListKeshkici враћа Кешкиће видљиве у датом месецу. Потрошња је кумулативна до краја тог месеца.
func (s *Store) ListKeshkici(month string) ([]Keshkic, error) {
	rows, err := s.db.Query(`
		SELECT k.id, k.month, k.dropped_month, k.title, k.amount, k.currency, k.rate, k.amount_rsd, k.is_default, k.created_at,
		       COALESCE(SUM(t.amount_rsd), 0) AS spent_rsd,
		       COUNT(t.id) AS tx_count
		FROM keshkici k
		LEFT JOIN transactions t ON t.keshkic_id = k.id AND t.month <= ?
		WHERE `+keshkicVisible+`
		GROUP BY k.id
		ORDER BY (k.dropped_month <> '' AND k.dropped_month <= ?) ASC, k.is_default DESC, k.id ASC`,
		month, month, month, month)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Keshkic{}
	for rows.Next() {
		k, err := scanKeshkic(rows, month)
		if err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

func (s *Store) GetKeshkic(id int64) (Keshkic, error) {
	k, err := scanKeshkic(s.db.QueryRow(`
		SELECT k.id, k.month, k.dropped_month, k.title, k.amount, k.currency, k.rate, k.amount_rsd, k.is_default, k.created_at,
		       COALESCE(SUM(t.amount_rsd), 0) AS spent_rsd,
		       COUNT(t.id) AS tx_count
		FROM keshkici k
		LEFT JOIN transactions t ON t.keshkic_id = k.id
		WHERE k.id = ?
		GROUP BY k.id`, id), "9999-12")
	if errors.Is(err, sql.ErrNoRows) {
		return k, errNotFound
	}
	return k, err
}

// ensureDefault осигурава да међу активним Кешкићима постоји подразумевани.
func (s *Store) ensureDefault() {
	var n int
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM keshkici WHERE is_default = 1 AND dropped_month = ''`).Scan(&n)
	if n == 0 {
		_, _ = s.db.Exec(`
			UPDATE keshkici SET is_default = 1
			WHERE id = (SELECT id FROM keshkici WHERE dropped_month = '' ORDER BY id LIMIT 1)`)
	}
}

func (s *Store) CreateKeshkic(k Keshkic) (Keshkic, error) {
	var active int
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM keshkici WHERE dropped_month = ''`).Scan(&active)

	isDefault := 0
	if active == 0 || k.IsDefault {
		isDefault = 1
		_, _ = s.db.Exec(`UPDATE keshkici SET is_default = 0`)
	}

	res, err := s.db.Exec(`
		INSERT INTO keshkici (month, title, amount, currency, rate, amount_rsd, is_default)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		k.Month, k.Title, k.Amount, k.Currency, k.Rate, k.AmountRSD, isDefault)
	if err != nil {
		return k, err
	}
	id, _ := res.LastInsertId()
	return s.GetKeshkic(id)
}

func (s *Store) UpdateKeshkic(k Keshkic) (Keshkic, error) {
	isDefInt := 0
	if k.IsDefault {
		isDefInt = 1
		_, _ = s.db.Exec(`UPDATE keshkici SET is_default = 0 WHERE id <> ?`, k.ID)
	}

	res, err := s.db.Exec(`
		UPDATE keshkici
		SET title = ?, amount = ?, currency = ?, rate = ?, amount_rsd = ?, is_default = ?
		WHERE id = ?`,
		k.Title, k.Amount, k.Currency, k.Rate, k.AmountRSD, isDefInt, k.ID)
	if err != nil {
		return k, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return k, errNotFound
	}
	s.ensureDefault()
	return s.GetKeshkic(k.ID)
}

func (s *Store) SetDefaultKeshkic(id int64) error {
	res, err := s.db.Exec(`UPDATE keshkici SET is_default = CASE WHEN id = ? THEN 1 ELSE 0 END WHERE dropped_month = '' OR id = ?`, id, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errNotFound
	}
	return nil
}

// DropKeshkic "одбацује" Кешкић: последњи месец у ком се приказује је `month`.
// Подаци и трошкови остају сачувани. month == "" враћа Кешкић у употребу.
func (s *Store) DropKeshkic(id int64, month string) error {
	res, err := s.db.Exec(`
		UPDATE keshkici
		SET dropped_month = ?, is_default = CASE WHEN ? <> '' THEN 0 ELSE is_default END
		WHERE id = ?`, month, month, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errNotFound
	}
	s.ensureDefault()
	return nil
}

func (s *Store) DeleteKeshkic(id int64) error {
	// Експлицитно бришемо трошкове (без обзира на foreign_keys)
	if _, err := s.db.Exec(`DELETE FROM transactions WHERE keshkic_id = ?`, id); err != nil {
		return err
	}
	res, err := s.db.Exec(`DELETE FROM keshkici WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errNotFound
	}
	s.ensureDefault()
	return nil
}

// ---------------------------------------------------------------------
// Transactions
// ---------------------------------------------------------------------

func (s *Store) ListTx(month string, keshkicID int64) ([]Tx, error) {
	query := `
		SELECT t.id, t.keshkic_id, COALESCE(k.title, 'Непознат') AS keshkic_title,
		       t.month, t.date, t.title, t.amount, t.currency, t.rate, t.amount_rsd, t.created_at
		FROM transactions t
		LEFT JOIN keshkici k ON k.id = t.keshkic_id
		WHERE t.month = ?`
	args := []any{month}

	if keshkicID > 0 {
		query += ` AND t.keshkic_id = ?`
		args = append(args, keshkicID)
	}

	query += ` ORDER BY t.date DESC, t.id DESC`

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Tx{}
	for rows.Next() {
		var t Tx
		if err := rows.Scan(&t.ID, &t.KeshkicID, &t.KeshkicTitle, &t.Month, &t.Date, &t.Title, &t.Amount, &t.Currency, &t.Rate, &t.AmountRSD, &t.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Store) GetTx(id int64) (Tx, error) {
	var t Tx
	err := s.db.QueryRow(`
		SELECT t.id, t.keshkic_id, COALESCE(k.title, 'Непознат') AS keshkic_title,
		       t.month, t.date, t.title, t.amount, t.currency, t.rate, t.amount_rsd, t.created_at
		FROM transactions t
		LEFT JOIN keshkici k ON k.id = t.keshkic_id
		WHERE t.id = ?`, id).
		Scan(&t.ID, &t.KeshkicID, &t.KeshkicTitle, &t.Month, &t.Date, &t.Title, &t.Amount, &t.Currency, &t.Rate, &t.AmountRSD, &t.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return t, errNotFound
	}
	return t, err
}

func (s *Store) CreateTx(t Tx) (Tx, error) {
	var exists int
	err := s.db.QueryRow(`SELECT 1 FROM keshkici WHERE id = ?`, t.KeshkicID).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return t, fmt.Errorf("изабрани Кешкић не постоји")
	}

	res, err := s.db.Exec(`
		INSERT INTO transactions (keshkic_id, month, date, title, amount, currency, rate, amount_rsd)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		t.KeshkicID, t.Date[:7], t.Date, t.Title, t.Amount, t.Currency, t.Rate, t.AmountRSD)
	if err != nil {
		return t, err
	}
	id, _ := res.LastInsertId()
	return s.GetTx(id)
}

func (s *Store) UpdateTx(t Tx) (Tx, error) {
	res, err := s.db.Exec(`
		UPDATE transactions
		SET keshkic_id = ?, month = ?, date = ?, title = ?, amount = ?, currency = ?, rate = ?, amount_rsd = ?
		WHERE id = ?`,
		t.KeshkicID, t.Date[:7], t.Date, t.Title, t.Amount, t.Currency, t.Rate, t.AmountRSD, t.ID)
	if err != nil {
		return t, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return t, errNotFound
	}
	return s.GetTx(t.ID)
}

func (s *Store) DeleteTx(id int64) error {
	res, err := s.db.Exec(`DELETE FROM transactions WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errNotFound
	}
	return nil
}

// ---------------------------------------------------------------------
// Month totals & history
// ---------------------------------------------------------------------

type MonthTotals struct {
	TotalKeshkic float64
	Expenses     float64
	Balance      float64
	TxCount      int
}

// MonthTotals: Кешкићи видљиви у месецу, њихова кумулативна потрошња до краја месеца.
func (s *Store) MonthTotals(month string) (m MonthTotals, err error) {
	err = s.db.QueryRow(`
		SELECT
			COALESCE((SELECT SUM(k.amount_rsd) FROM keshkici k WHERE `+keshkicVisible+`), 0),
			COALESCE((SELECT SUM(t.amount_rsd) FROM transactions t
			          JOIN keshkici k ON k.id = t.keshkic_id
			          WHERE t.month <= ? AND `+keshkicVisible+`), 0),
			COALESCE((SELECT COUNT(*) FROM transactions WHERE month = ?), 0)
	`, month, month, month, month, month, month).Scan(&m.TotalKeshkic, &m.Expenses, &m.TxCount)
	m.Balance = m.TotalKeshkic - m.Expenses
	return
}
