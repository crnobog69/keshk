package main

import (
	"log"
	"time"
)

// ---------------------------------------------------------------------
// Пробни (demo) подаци — KESHK_SEED=true
//
// Попуњава празну базу са неколико Кешкића, трошковима у три месеца и
// листама за куповину, да се изглед апликације може погледати одмах.
// Датуми се рачунају у односу на данашњи дан. Ако база већ има Кешкиће
// или листе, ништа се не мења.
// ---------------------------------------------------------------------

func (s *Store) isEmpty() bool {
	var k, l int
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM keshkici`).Scan(&k)
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM shopping_lists`).Scan(&l)
	return k == 0 && l == 0
}

type demoItem struct {
	name    string
	price   float64 // 0 = без цене
	checked bool
}

func seedDemo(s *Store, now time.Time) error {
	if !s.isEmpty() {
		log.Println("[INFO] KESHK_SEED: база већ има податке — пробни подаци нису додати (користи празну базу, нпр. DB_PATH=data/demo.db)")
		return nil
	}

	// month(-1) = претходни месец у формату YYYY-MM
	first := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
	month := func(delta int) string { return first.AddDate(0, delta, 0).Format("2006-01") }
	// date: дан у месецу (delta), ограничен на дужину месеца, а за текући месец и на данашњи дан
	date := func(delta, day int) string {
		m := first.AddDate(0, delta, 0)
		last := m.AddDate(0, 1, -1).Day()
		if delta == 0 && day > now.Day() {
			day = now.Day()
		}
		if day > last {
			day = last
		}
		return time.Date(m.Year(), m.Month(), day, 0, 0, 0, 0, m.Location()).Format("2006-01-02")
	}

	newK := func(delta int, title string, amount float64, cur string, rate float64, def bool) (Keshkic, error) {
		return s.CreateKeshkic(Keshkic{
			Month: month(delta), Title: title, Amount: amount, Currency: cur, Rate: rate,
			AmountRSD: round2(amount * rate), IsDefault: def,
		})
	}

	mama, err := newK(-2, "Мама", 30000, "RSD", 1, true)
	if err != nil {
		return err
	}
	games, err := newK(-2, "За игре", 40, "EUR", 117.2, false)
	if err != nil {
		return err
	}
	saving, err := newK(-1, "Уштеђевина", 100, "USD", 99.4, false)
	if err != nil {
		return err
	}
	trip, err := newK(-2, "Излет", 15000, "RSD", 1, false)
	if err != nil {
		return err
	}
	if err := s.DropKeshkic(trip.ID, month(-1)); err != nil {
		return err
	}

	txs := []struct {
		k      Keshkic
		delta  int
		day    int
		title  string
		amount float64
		cur    string
		rate   float64
	}{
		{mama, -2, 3, "Намирнице", 3200, "RSD", 1},
		{mama, -2, 7, "Превоз", 1500, "RSD", 1},
		{mama, -2, 12, "Кафа са друштвом", 850, "RSD", 1},
		{mama, -2, 20, "Ручак", 1250, "RSD", 1},
		{mama, -2, 26, "Рачун за струју", 4800, "RSD", 1},
		{mama, -1, 2, "Намирнице", 4100, "RSD", 1},
		{mama, -1, 9, "Сервис бицикла", 2300, "RSD", 1},
		{mama, -1, 15, "Позориште", 1800, "RSD", 1},
		{mama, -1, 22, "Намирнице", 3650, "RSD", 1},
		{mama, -1, 28, "Поклон", 2500, "RSD", 1},
		{mama, 0, 1, "Превоз", 1500, "RSD", 1},
		{mama, 0, 1, "Кафа", 400, "RSD", 1},
		{mama, 0, 2, "Намирнице", 2900, "RSD", 1},
		{games, -2, 5, "Steam", 12.99, "EUR", 117.2},
		{games, -1, 11, "Game Pass", 9.99, "EUR", 117.5},
		{games, -1, 24, "Нова игра", 19.99, "EUR", 117.4},
		{games, 0, 3, "DLC", 7.49, "EUR", 117.2},
		{saving, -1, 14, "Онлајн курс", 15, "USD", 99.4},
		{saving, 0, 2, "Књига", 12.5, "USD", 99.8},
		{trip, -2, 18, "Карта за воз", 3200, "RSD", 1},
		{trip, -1, 4, "Хостел", 5400, "RSD", 1},
	}
	for _, t := range txs {
		if _, err := s.CreateTx(Tx{
			KeshkicID: t.k.ID, Date: date(t.delta, t.day), Title: t.title,
			Amount: t.amount, Currency: t.cur, Rate: t.rate, AmountRSD: round2(t.amount * t.rate),
		}); err != nil {
			return err
		}
	}

	newList := func(title string, keshkicID int64, due string, items []demoItem) (ShoppingList, error) {
		l, err := s.CreateShoppingList(ShoppingList{Title: title, KeshkicID: keshkicID, Currency: "RSD", DueDate: due})
		if err != nil {
			return l, err
		}
		for _, it := range items {
			var p *float64
			if it.price > 0 {
				v := it.price
				p = &v
			}
			if err := s.AddShoppingItem(l.ID, it.name, p); err != nil {
				return l, err
			}
		}
		full, err := s.GetShoppingList(l.ID)
		if err != nil {
			return l, err
		}
		for i, it := range items {
			if !it.checked {
				continue
			}
			si := full.Items[i]
			si.Checked = true
			if err := s.UpdateShoppingItem(si); err != nil {
				return l, err
			}
		}
		return s.GetShoppingList(l.ID)
	}

	// 1) Отворена листа са роком за 2 дана
	due := now.AddDate(0, 0, 2).Format("2006-01-02")
	if _, err := newList("Недељна куповина", mama.ID, due, []demoItem{
		{"Млеко", 129.90, true},
		{"Хлеб", 65, true},
		{"Јаја (10 комада)", 289, false},
		{"Кафа", 799, true},
		{"Сир", 0, false},
	}); err != nil {
		return err
	}

	// 2) Већ одузета листа (прави трошак од збира купљених ставки)
	l2, err := newList("Месечна куповина", mama.ID, "", []demoItem{
		{"Пиринач", 245, true},
		{"Тестенина", 180, true},
		{"Уље", 330, true},
		{"Детерџент", 520, true},
	})
	if err != nil {
		return err
	}
	charged, err := s.CreateTx(Tx{
		KeshkicID: mama.ID, Date: date(-1, 5), Title: l2.Title,
		Amount: l2.Total, Currency: "RSD", Rate: 1, AmountRSD: l2.Total,
	})
	if err != nil {
		return err
	}
	if err := s.SetListTx(l2.ID, charged.ID, mama.ID); err != nil {
		return err
	}

	// 3) Листа без рока и без цена
	if _, err := newList("Рођендан", 0, "", []demoItem{
		{"Торта", 2400, false},
		{"Балони", 0, false},
		{"Свеће", 0, false},
	}); err != nil {
		return err
	}

	log.Printf("[INFO] KESHK_SEED: додато 4 Кешкића, %d трошкова и 3 листе", len(txs)+1)
	return nil
}
