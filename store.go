package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
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
	SpentRSD     float64 `json:"spentRsd"`     // total expenses deducted up to the viewed month
	DepositsRSD  float64 `json:"depositsRsd"`  // total deposits (уплате) added up to the viewed month
	Balance      float64 `json:"balance"`      // AmountRSD + DepositsRSD - SpentRSD
	TxCount      int     `json:"txCount"`      // number of expenses up to the viewed month
	DepositCount int     `json:"depositCount"` // number of deposits up to the viewed month
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

// Deposit је уплата (додатни новац) на одређени Кешкић, нпр. мама уплати још 2.000 RSD.
// Увећава буџет Кешкића од месеца уплате па надаље, исто као што трошкови умањују преостали износ.
type Deposit struct {
	ID           int64   `json:"id"`
	KeshkicID    int64   `json:"keshkicId"`
	KeshkicTitle string  `json:"keshkicTitle,omitempty"`
	Month        string  `json:"month"` // YYYY-MM
	Date         string  `json:"date"`  // YYYY-MM-DD
	Title        string  `json:"title"` // од кога / опис
	Amount       float64 `json:"amount"`
	Currency     string  `json:"currency"`
	Rate         float64 `json:"rate"`
	AmountRSD    float64 `json:"amountRsd"`
	CreatedAt    string  `json:"createdAt"`
}

type Store struct{ db *sql.DB }

var errNotFound = errors.New("not found")

func openPG(connStr string, maxOpen, maxIdle int) (*sql.DB, error) {
	cfg, err := pgx.ParseConfig(connStr)
	if err != nil {
		return nil, fmt.Errorf("неисправан URL базе: %w", err)
	}
	// Neon pooled конекција користи PgBouncer у transaction режиму,
	// па се именовани prepared statements не смеју кеширати по конекцији.
	cfg.DefaultQueryExecMode = pgx.QueryExecModeExec

	var opts []stdlib.OptionOpenDB
	if sp := strings.TrimSpace(cfg.RuntimeParams["search_path"]); sp != "" {
		delete(cfg.RuntimeParams, "search_path")
		ident := pgx.Identifier{sp}.Sanitize()
		opts = append(opts, stdlib.OptionAfterConnect(func(ctx context.Context, conn *pgx.Conn) error {
			_, err := conn.Exec(ctx, "SET search_path TO "+ident)
			return err
		}))
	}

	db := stdlib.OpenDB(*cfg, opts...)
	db.SetMaxOpenConns(maxOpen)
	db.SetMaxIdleConns(maxIdle)
	// Затварај неактивне конекције пре Neon-овог рока за успављивање (scale-to-zero).
	db.SetConnMaxIdleTime(2 * time.Minute)
	db.SetConnMaxLifetime(30 * time.Minute)
	return db, nil
}

func openStore(databaseURL, directURL string) (*Store, error) {
	databaseURL = strings.TrimSpace(databaseURL)
	directURL = strings.TrimSpace(directURL)
	if databaseURL == "" {
		return nil, errors.New("DATABASE_URL није постављен")
	}
	if directURL == "" {
		directURL = databaseURL
	}

	// DDL миграције иду преко директне (unpooled) конекције.
	migDB, err := openPG(directURL, 2, 1)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := migDB.PingContext(ctx); err != nil {
		_ = migDB.Close()
		return nil, fmt.Errorf("повезивање са Neon базом (миграција): %w", err)
	}
	migStore := &Store{db: migDB}
	if err := migStore.migrate(); err != nil {
		_ = migDB.Close()
		return nil, fmt.Errorf("миграција базе: %w", err)
	}
	_ = migDB.Close()

	// Редовни упити користе pooled конекцију (DATABASE_URL).
	db, err := openPG(databaseURL, 10, 4)
	if err != nil {
		return nil, err
	}
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("повезивање са Neon базом: %w", err)
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

const utcCreatedAtDefault = `to_char(now() AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"')`

func (s *Store) migrate() error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS users (
			id                    BIGINT GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY,
			username              TEXT    NOT NULL,
			password_hash         TEXT    NOT NULL,
			display_name          TEXT    NOT NULL DEFAULT '',
			avatar_url            TEXT    NOT NULL DEFAULT '',
			banner_url            TEXT    NOT NULL DEFAULT '',
			bio                   TEXT    NOT NULL DEFAULT '',
			member_since          TEXT    NOT NULL DEFAULT '',
			is_admin              INTEGER NOT NULL DEFAULT 0,
			deletion_code         TEXT    NOT NULL DEFAULT '',
			deletion_requested_at TEXT    NOT NULL DEFAULT '',
			created_at            TEXT    NOT NULL DEFAULT ` + utcCreatedAtDefault + `
		)`,
		`ALTER TABLE users ADD COLUMN IF NOT EXISTS deletion_code TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE users ADD COLUMN IF NOT EXISTS deletion_requested_at TEXT NOT NULL DEFAULT ''`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_users_username_lower ON users (LOWER(username))`,
		`CREATE INDEX IF NOT EXISTS idx_users_admin_id ON users (is_admin DESC, id ASC)`,
		`CREATE INDEX IF NOT EXISTS idx_users_display_lower ON users (LOWER(display_name))`,
		`CREATE INDEX IF NOT EXISTS idx_users_pending_deletion ON users (deletion_requested_at DESC, id DESC) WHERE deletion_code <> ''`,
		`CREATE TABLE IF NOT EXISTS invite_codes (
			id         BIGINT GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY,
			code       TEXT    NOT NULL UNIQUE,
			created_by BIGINT  NOT NULL,
			used_by    BIGINT,
			used_at    TEXT    NOT NULL DEFAULT '',
			created_at TEXT    NOT NULL DEFAULT ` + utcCreatedAtDefault + `
		)`,
		`CREATE INDEX IF NOT EXISTS idx_invites_active_id ON invite_codes ((used_at <> '' OR used_by IS NOT NULL) ASC, id DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_invites_used_by ON invite_codes (used_by) WHERE used_by IS NOT NULL`,
		`CREATE TABLE IF NOT EXISTS keshkici (
			id            BIGINT GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY,
			user_id       BIGINT           NOT NULL DEFAULT 1,
			month         TEXT             NOT NULL,
			dropped_month TEXT             NOT NULL DEFAULT '',
			title         TEXT             NOT NULL,
			amount        DOUBLE PRECISION NOT NULL,
			currency      TEXT             NOT NULL DEFAULT 'RSD',
			rate          DOUBLE PRECISION NOT NULL DEFAULT 1,
			amount_rsd    DOUBLE PRECISION NOT NULL,
			is_default    INTEGER          NOT NULL DEFAULT 0,
			created_at    TEXT             NOT NULL DEFAULT ` + utcCreatedAtDefault + `
		)`,
		`ALTER TABLE keshkici ADD COLUMN IF NOT EXISTS user_id BIGINT NOT NULL DEFAULT 1`,
		`DROP INDEX IF EXISTS idx_keshkici_month`,
		`CREATE INDEX IF NOT EXISTS idx_keshkici_user_month ON keshkici(user_id, month)`,
		`CREATE INDEX IF NOT EXISTS idx_keshkici_user_active_def ON keshkici(user_id, is_default DESC, id ASC) WHERE dropped_month = ''`,
		`CREATE TABLE IF NOT EXISTS transactions (
			id          BIGINT GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY,
			user_id     BIGINT           NOT NULL DEFAULT 1,
			keshkic_id  BIGINT           NOT NULL REFERENCES keshkici(id) ON DELETE CASCADE,
			month       TEXT             NOT NULL,
			date        TEXT             NOT NULL,
			title       TEXT             NOT NULL DEFAULT '',
			amount      DOUBLE PRECISION NOT NULL,
			currency    TEXT             NOT NULL DEFAULT 'RSD',
			rate        DOUBLE PRECISION NOT NULL DEFAULT 1,
			amount_rsd  DOUBLE PRECISION NOT NULL,
			created_at  TEXT             NOT NULL DEFAULT ` + utcCreatedAtDefault + `
		)`,
		`ALTER TABLE transactions ADD COLUMN IF NOT EXISTS user_id BIGINT NOT NULL DEFAULT 1`,
		`DROP INDEX IF EXISTS idx_tx_month`,
		`CREATE INDEX IF NOT EXISTS idx_tx_user_month_date ON transactions(user_id, month, date DESC, id DESC)`,
		`DROP INDEX IF EXISTS idx_tx_user_month`,
		`CREATE INDEX IF NOT EXISTS idx_tx_keshkic ON transactions(keshkic_id)`,
		`CREATE INDEX IF NOT EXISTS idx_tx_user_keshkic_month ON transactions(user_id, keshkic_id, month)`,
		`CREATE TABLE IF NOT EXISTS deposits (
			id          BIGINT GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY,
			user_id     BIGINT           NOT NULL,
			keshkic_id  BIGINT           NOT NULL REFERENCES keshkici(id) ON DELETE CASCADE,
			month       TEXT             NOT NULL,
			date        TEXT             NOT NULL,
			title       TEXT             NOT NULL DEFAULT '',
			amount      DOUBLE PRECISION NOT NULL,
			currency    TEXT             NOT NULL DEFAULT 'RSD',
			rate        DOUBLE PRECISION NOT NULL DEFAULT 1,
			amount_rsd  DOUBLE PRECISION NOT NULL,
			created_at  TEXT             NOT NULL DEFAULT ` + utcCreatedAtDefault + `
		)`,
		`CREATE INDEX IF NOT EXISTS idx_dep_user_month_date ON deposits(user_id, month, date DESC, id DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_dep_keshkic_month ON deposits(keshkic_id, month)`,
	}
	for _, q := range stmts {
		if _, err := s.db.Exec(q); err != nil {
			return err
		}
	}
	return s.migrateExtra()
}

// ---------------------------------------------------------------------
// Keshkici
// ---------------------------------------------------------------------

// Кешкић је видљив у месецу M ($2) за корисника ($1) ако је почео <= M и није одбачен пре M.
const keshkicVisible = `k.user_id = $1 AND k.month <= $2 AND (k.dropped_month = '' OR k.dropped_month >= $2)`

// keshkicSelect враћа Кешкиће са кумулативном потрошњом и уплатама до краја месеца $2 за корисника $1.
// Позивалац дописује WHERE / ORDER BY.
const keshkicSelect = `
	SELECT k.id, k.month, k.dropped_month, k.title, k.amount, k.currency, k.rate, k.amount_rsd, k.is_default, k.created_at,
	       tx.spent, tx.cnt, dp.total, dp.cnt
	FROM keshkici k
	CROSS JOIN LATERAL (
		SELECT COALESCE(SUM(t.amount_rsd), 0) AS spent, COUNT(*) AS cnt
		FROM transactions t
		WHERE t.keshkic_id = k.id AND t.user_id = $1 AND t.month <= $2
	) tx
	CROSS JOIN LATERAL (
		SELECT COALESCE(SUM(d.amount_rsd), 0) AS total, COUNT(*) AS cnt
		FROM deposits d
		WHERE d.keshkic_id = k.id AND d.user_id = $1 AND d.month <= $2
	) dp`

func scanKeshkic(sc interface{ Scan(...any) error }, viewMonth string) (Keshkic, error) {
	var k Keshkic
	var isDef int
	err := sc.Scan(&k.ID, &k.Month, &k.DroppedMonth, &k.Title, &k.Amount, &k.Currency, &k.Rate,
		&k.AmountRSD, &isDef, &k.CreatedAt, &k.SpentRSD, &k.TxCount, &k.DepositsRSD, &k.DepositCount)
	if err != nil {
		return k, err
	}
	k.IsDefault = isDef == 1
	k.Dropped = k.DroppedMonth != "" && viewMonth >= k.DroppedMonth
	k.Balance = k.AmountRSD + k.DepositsRSD - k.SpentRSD
	return k, nil
}

// ListKeshkici враћа Кешкиће видљиве у датом месецу за датог корисника. Потрошња и уплате су кумулативне до краја тог месеца.
func (s *Store) ListKeshkici(userID int64, month string) ([]Keshkic, error) {
	rows, err := s.db.Query(keshkicSelect+`
		WHERE `+keshkicVisible+`
		ORDER BY (k.dropped_month <> '' AND k.dropped_month <= $2) ASC, k.is_default DESC, k.id ASC`,
		userID, month)
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

func (s *Store) GetKeshkic(userID, id int64) (Keshkic, error) {
	k, err := scanKeshkic(s.db.QueryRow(keshkicSelect+`
		WHERE k.id = $3 AND k.user_id = $1`, userID, "9999-12", id), "9999-12")
	if errors.Is(err, sql.ErrNoRows) {
		return k, errNotFound
	}
	return k, err
}

// ensureDefault осигурава да међу активним Кешкићима корисника постоји подразумевани.
func (s *Store) ensureDefault(userID int64) {
	var n int
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM keshkici WHERE user_id = $1 AND is_default = 1 AND dropped_month = ''`, userID).Scan(&n)
	if n == 0 {
		_, _ = s.db.Exec(`
			UPDATE keshkici SET is_default = 1
			WHERE id = (SELECT id FROM keshkici WHERE user_id = $1 AND dropped_month = '' ORDER BY id LIMIT 1)`, userID)
	}
}

func (s *Store) CreateKeshkic(userID int64, k Keshkic) (Keshkic, error) {
	var active int
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM keshkici WHERE user_id = $1 AND dropped_month = ''`, userID).Scan(&active)

	isDefault := 0
	if active == 0 || k.IsDefault {
		isDefault = 1
		_, _ = s.db.Exec(`UPDATE keshkici SET is_default = 0 WHERE user_id = $1`, userID)
	}

	var id int64
	err := s.db.QueryRow(`
		INSERT INTO keshkici (user_id, month, title, amount, currency, rate, amount_rsd, is_default)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING id`,
		userID, k.Month, k.Title, k.Amount, k.Currency, k.Rate, k.AmountRSD, isDefault).Scan(&id)
	if err != nil {
		return k, err
	}
	return s.GetKeshkic(userID, id)
}

func (s *Store) UpdateKeshkic(userID int64, k Keshkic) (Keshkic, error) {
	isDefInt := 0
	if k.IsDefault {
		isDefInt = 1
		_, _ = s.db.Exec(`UPDATE keshkici SET is_default = 0 WHERE user_id = $1 AND id <> $2`, userID, k.ID)
	}

	res, err := s.db.Exec(`
		UPDATE keshkici
		SET title = $1, amount = $2, currency = $3, rate = $4, amount_rsd = $5, is_default = $6
		WHERE id = $7 AND user_id = $8`,
		k.Title, k.Amount, k.Currency, k.Rate, k.AmountRSD, isDefInt, k.ID, userID)
	if err != nil {
		return k, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return k, errNotFound
	}
	s.ensureDefault(userID)
	return s.GetKeshkic(userID, k.ID)
}

func (s *Store) SetDefaultKeshkic(userID, id int64) error {
	if _, err := s.GetKeshkic(userID, id); err != nil {
		return err
	}
	res, err := s.db.Exec(`UPDATE keshkici SET is_default = CASE WHEN id = $1 THEN 1 ELSE 0 END WHERE user_id = $2 AND (dropped_month = '' OR id = $1)`, id, userID)
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
func (s *Store) DropKeshkic(userID, id int64, month string) error {
	res, err := s.db.Exec(`
		UPDATE keshkici
		SET dropped_month = $1, is_default = CASE WHEN $1::text <> '' THEN 0 ELSE is_default END
		WHERE id = $2 AND user_id = $3`, month, id, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errNotFound
	}
	s.ensureDefault(userID)
	return nil
}

func (s *Store) DeleteKeshkic(userID, id int64) error {
	if _, err := s.db.Exec(`DELETE FROM transactions WHERE keshkic_id = $1 AND user_id = $2`, id, userID); err != nil {
		return err
	}
	if _, err := s.db.Exec(`DELETE FROM deposits WHERE keshkic_id = $1 AND user_id = $2`, id, userID); err != nil {
		return err
	}
	res, err := s.db.Exec(`DELETE FROM keshkici WHERE id = $1 AND user_id = $2`, id, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errNotFound
	}
	s.ensureDefault(userID)
	return nil
}

// ---------------------------------------------------------------------
// Transactions
// ---------------------------------------------------------------------

func (s *Store) ListTx(userID int64, month string, keshkicID int64) ([]Tx, error) {
	query := `
		SELECT t.id, t.keshkic_id, COALESCE(k.title, 'Непознат') AS keshkic_title,
		       t.month, t.date, t.title, t.amount, t.currency, t.rate, t.amount_rsd, t.created_at
		FROM transactions t
		LEFT JOIN keshkici k ON k.id = t.keshkic_id AND k.user_id = $1
		WHERE t.user_id = $1 AND t.month = $2`
	args := []any{userID, month}

	if keshkicID > 0 {
		query += ` AND t.keshkic_id = $3`
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

func (s *Store) GetTx(userID, id int64) (Tx, error) {
	var t Tx
	err := s.db.QueryRow(`
		SELECT t.id, t.keshkic_id, COALESCE(k.title, 'Непознат') AS keshkic_title,
		       t.month, t.date, t.title, t.amount, t.currency, t.rate, t.amount_rsd, t.created_at
		FROM transactions t
		LEFT JOIN keshkici k ON k.id = t.keshkic_id AND k.user_id = $2
		WHERE t.id = $1 AND t.user_id = $2`, id, userID).
		Scan(&t.ID, &t.KeshkicID, &t.KeshkicTitle, &t.Month, &t.Date, &t.Title, &t.Amount, &t.Currency, &t.Rate, &t.AmountRSD, &t.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return t, errNotFound
	}
	return t, err
}

func (s *Store) CreateTx(userID int64, t Tx) (Tx, error) {
	var exists int
	err := s.db.QueryRow(`SELECT 1 FROM keshkici WHERE id = $1 AND user_id = $2`, t.KeshkicID, userID).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return t, fmt.Errorf("изабрани Кешкић не постоји")
	}

	var id int64
	err = s.db.QueryRow(`
		INSERT INTO transactions (user_id, keshkic_id, month, date, title, amount, currency, rate, amount_rsd)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING id`,
		userID, t.KeshkicID, t.Date[:7], t.Date, t.Title, t.Amount, t.Currency, t.Rate, t.AmountRSD).Scan(&id)
	if err != nil {
		return t, err
	}
	return s.GetTx(userID, id)
}

func (s *Store) UpdateTx(userID int64, t Tx) (Tx, error) {
	var exists int
	if err := s.db.QueryRow(`SELECT 1 FROM keshkici WHERE id = $1 AND user_id = $2`, t.KeshkicID, userID).Scan(&exists); errors.Is(err, sql.ErrNoRows) {
		return t, fmt.Errorf("изабрани Кешкић не постоји")
	}
	res, err := s.db.Exec(`
		UPDATE transactions
		SET keshkic_id = $1, month = $2, date = $3, title = $4, amount = $5, currency = $6, rate = $7, amount_rsd = $8
		WHERE id = $9 AND user_id = $10`,
		t.KeshkicID, t.Date[:7], t.Date, t.Title, t.Amount, t.Currency, t.Rate, t.AmountRSD, t.ID, userID)
	if err != nil {
		return t, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return t, errNotFound
	}
	return s.GetTx(userID, t.ID)
}

func (s *Store) DeleteTx(userID, id int64) error {
	res, err := s.db.Exec(`DELETE FROM transactions WHERE id = $1 AND user_id = $2`, id, userID)
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
	TotalKeshkic float64 // почетни износи Кешкића + уплате
	Deposits     float64 // од тога уплате
	Expenses     float64
	Balance      float64
	TxCount      int
}

// MonthTotals: Кешкићи видљиви у месецу за корисника, њихове кумулативне уплате и потрошња до краја месеца.
func (s *Store) MonthTotals(userID int64, month string) (m MonthTotals, err error) {
	err = s.db.QueryRow(`
		SELECT
			COALESCE((SELECT SUM(k.amount_rsd) FROM keshkici k WHERE `+keshkicVisible+`), 0),
			COALESCE((SELECT SUM(d.amount_rsd) FROM deposits d
			          JOIN keshkici k ON k.id = d.keshkic_id AND k.user_id = $1
			          WHERE d.user_id = $1 AND d.month <= $2 AND `+keshkicVisible+`), 0),
			COALESCE((SELECT SUM(t.amount_rsd) FROM transactions t
			          JOIN keshkici k ON k.id = t.keshkic_id AND k.user_id = $1
			          WHERE t.user_id = $1 AND t.month <= $2 AND `+keshkicVisible+`), 0),
			COALESCE((SELECT COUNT(*) FROM transactions WHERE user_id = $1 AND month = $2), 0)
	`, userID, month).Scan(&m.TotalKeshkic, &m.Deposits, &m.Expenses, &m.TxCount)
	m.TotalKeshkic += m.Deposits
	m.Balance = m.TotalKeshkic - m.Expenses
	return
}
