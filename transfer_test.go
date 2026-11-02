package main

import (
	"testing"
)

func TestBackupRoundTrip(t *testing.T) {
	s := openTestStore(t)

	k, err := s.CreateKeshkic(1, Keshkic{Month: "2026-11", Title: "Мама", Amount: 5000, Currency: "RSD", Rate: 1, AmountRSD: 5000, IsDefault: true})
	if err != nil {
		t.Fatal(err)
	}
	tx, err := s.CreateTx(1, Tx{KeshkicID: k.ID, Date: "2026-11-01", Title: "кафа", Amount: 100, Currency: "EUR", Rate: 117, AmountRSD: 11700})
	if err != nil {
		t.Fatal(err)
	}
	l, err := s.CreateShoppingList(1, ShoppingList{Title: "Листа", Currency: "RSD"})
	if err != nil {
		t.Fatal(err)
	}
	price := 120.0
	if err := s.AddShoppingItem(1, l.ID, "млеко", &price); err != nil {
		t.Fatal(err)
	}
	if err := s.SetListTx(1, l.ID, tx.ID, k.ID); err != nil {
		t.Fatal(err)
	}

	b, err := s.ExportBackup(1)
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Keshkici) != 1 || len(b.Transactions) != 1 || len(b.Lists) != 1 || len(b.Lists[0].Items) != 1 {
		t.Fatalf("неочекиван садржај извоза: %+v", b)
	}

	// Промени податке, па врати из копије.
	if err := s.DeleteKeshkic(1, k.ID); err != nil {
		t.Fatal(err)
	}
	if err := b.normalize(); err != nil {
		t.Fatal(err)
	}
	if err := s.ImportBackup(1, b); err != nil {
		t.Fatal(err)
	}

	got, err := s.ExportBackup(1)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Keshkici) != 1 || len(got.Transactions) != 1 || got.Transactions[0].Amount != 100 {
		t.Fatalf("увоз није вратио податке: %+v", got)
	}
	if got.Lists[0].TxID != tx.ID || got.Lists[0].KeshkicID != k.ID || len(got.Lists[0].Items) != 1 || !got.Keshkici[0].IsDefault {
		t.Fatalf("везе између листе, трошка и Кешкића нису очуване: %+v", got.Lists[0])
	}
	if sum, _ := s.MonthTotals(1, "2026-11"); sum.Expenses != 11700 {
		t.Fatalf("очекивано 11700 потрошено, добијено %v", sum.Expenses)
	}

	// Нови уноси након увоза морају добити наредне ИД-јеве без сукоба са увезеним записима.
	k2, err := s.CreateKeshkic(1, Keshkic{Month: "2026-11", Title: "Уштеђевина", Amount: 2000, Currency: "RSD", Rate: 1, AmountRSD: 2000})
	if err != nil {
		t.Fatalf("креирање Кешкића после увоза: %v", err)
	}
	if k2.ID <= k.ID {
		t.Fatalf("очекиван нови ID > %d, добијен %d", k.ID, k2.ID)
	}
}

func TestBackupMultiUserCollisionRemap(t *testing.T) {
	s := openTestStore(t)

	// Корисник 1 заузима ID=1 у keshkici, transactions, shopping_lists и shopping_items.
	k1, err := s.CreateKeshkic(1, Keshkic{Month: "2026-11", Title: "Админ", Amount: 10000, Currency: "RSD", Rate: 1, AmountRSD: 10000, IsDefault: true})
	if err != nil {
		t.Fatal(err)
	}
	tx1, err := s.CreateTx(1, Tx{KeshkicID: k1.ID, Date: "2026-11-01", Title: "Ручак", Amount: 900, Currency: "RSD", Rate: 1, AmountRSD: 900})
	if err != nil {
		t.Fatal(err)
	}
	l1, err := s.CreateShoppingList(1, ShoppingList{Title: "Админова листа", KeshkicID: k1.ID, Currency: "RSD"})
	if err != nil {
		t.Fatal(err)
	}
	p1 := 250.0
	if err := s.AddShoppingItem(1, l1.ID, "Хлеб", &p1); err != nil {
		t.Fatal(err)
	}
	if err := s.SetListTx(1, l1.ID, tx1.ID, k1.ID); err != nil {
		t.Fatal(err)
	}

	// Корисник 2 увози фајл чији се ИД-јеви поклапају са Корисником 1 чак и у обрнутом редоследу [ID=2, ID=1].
	p2 := 400.0
	b2 := Backup{
		App:     backupApp,
		Version: backupVersion,
		Keshkici: []backupKeshkic{
			{ID: 2, Month: "2026-11", Title: "Други Кешкић", Amount: 3000, Currency: "RSD", Rate: 1},
			{ID: k1.ID, Month: "2026-11", Title: "Први Кешкић", Amount: 7000, Currency: "RSD", Rate: 1, IsDefault: true},
		},
		Transactions: []backupTx{
			{ID: tx1.ID, KeshkicID: k1.ID, Date: "2026-11-02", Title: "Куповина", Amount: 400, Currency: "RSD", Rate: 1},
		},
		Lists: []backupList{
			{
				ID:        l1.ID,
				Title:     "Листа корисника 2",
				KeshkicID: k1.ID,
				Currency:  "RSD",
				TxID:      tx1.ID,
				Items: []backupItem{
					{ID: 1, Name: "Млеко", Price: &p2, Checked: true, Position: 0},
				},
			},
		},
	}
	if err := b2.normalize(); err != nil {
		t.Fatal(err)
	}
	if err := s.ImportBackup(2, b2); err != nil {
		t.Fatalf("ImportBackup за корисника 2 није успео: %v", err)
	}

	// Провери да су подаци Корисника 1 потпуно нетакнути.
	got1, err := s.ExportBackup(1)
	if err != nil {
		t.Fatal(err)
	}
	if len(got1.Keshkici) != 1 || got1.Keshkici[0].Title != "Админ" || len(got1.Lists) != 1 || got1.Lists[0].Title != "Админова листа" {
		t.Fatalf("подаци корисника 1 су промењени: %+v", got1)
	}

	// Провери да су подаци и међусобне везе Корисника 2 исправно премапирани.
	got2, err := s.ExportBackup(2)
	if err != nil {
		t.Fatal(err)
	}
	if len(got2.Keshkici) != 2 || len(got2.Transactions) != 1 || len(got2.Lists) != 1 || len(got2.Lists[0].Items) != 1 {
		t.Fatalf("неочекивани подаци корисника 2: %+v", got2)
	}
	var mappedFirstKID int64
	for _, k := range got2.Keshkici {
		if k.Title == "Први Кешкић" {
			mappedFirstKID = k.ID
		}
	}
	if mappedFirstKID == 0 || mappedFirstKID == k1.ID {
		t.Fatalf("ИД Кешкића није премапиран: %d", mappedFirstKID)
	}
	if got2.Transactions[0].KeshkicID != mappedFirstKID {
		t.Fatalf("трошак није повезан на премапирани Кешкић: %+v", got2.Transactions[0])
	}
	if got2.Lists[0].KeshkicID != mappedFirstKID || got2.Lists[0].TxID != got2.Transactions[0].ID {
		t.Fatalf("листа није повезана на премапирани Кешкић и трошак: %+v", got2.Lists[0])
	}
}

func TestBackupRejectsInvalid(t *testing.T) {
	cases := map[string]Backup{
		"погрешна апликација": {App: "other", Version: 1},
		"будућа верзија":      {App: backupApp, Version: 99},
		"трошак без Кешкића": {App: backupApp, Version: 1,
			Transactions: []backupTx{{ID: 1, KeshkicID: 7, Date: "2026-11-01", Amount: 1, Currency: "RSD"}}},
		"лош месец": {App: backupApp, Version: 1,
			Keshkici: []backupKeshkic{{ID: 1, Month: "2026-13", Amount: 1, Currency: "RSD"}}},
	}
	for name, b := range cases {
		if err := b.normalize(); err == nil {
			t.Errorf("%s: очекивана грешка", name)
		}
	}
}
