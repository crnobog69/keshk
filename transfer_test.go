package main

import (
	"path/filepath"
	"testing"
)

func TestBackupRoundTrip(t *testing.T) {
	s, err := openStore(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	k, err := s.CreateKeshkic(Keshkic{Month: "2026-11", Title: "Мама", Amount: 5000, Currency: "RSD", Rate: 1, AmountRSD: 5000, IsDefault: true})
	if err != nil {
		t.Fatal(err)
	}
	tx, err := s.CreateTx(Tx{KeshkicID: k.ID, Date: "2026-11-01", Title: "кафа", Amount: 100, Currency: "EUR", Rate: 117, AmountRSD: 11700})
	if err != nil {
		t.Fatal(err)
	}
	l, err := s.CreateShoppingList(ShoppingList{Title: "Листа", Currency: "RSD"})
	if err != nil {
		t.Fatal(err)
	}
	price := 120.0
	if err := s.AddShoppingItem(l.ID, "млеко", &price); err != nil {
		t.Fatal(err)
	}
	if err := s.SetListTx(l.ID, tx.ID, k.ID); err != nil {
		t.Fatal(err)
	}

	b, err := s.ExportBackup()
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Keshkici) != 1 || len(b.Transactions) != 1 || len(b.Lists) != 1 || len(b.Lists[0].Items) != 1 {
		t.Fatalf("неочекиван садржај извоза: %+v", b)
	}

	// Промени податке, па врати из копије.
	if err := s.DeleteKeshkic(k.ID); err != nil {
		t.Fatal(err)
	}
	if err := b.normalize(); err != nil {
		t.Fatal(err)
	}
	if err := s.ImportBackup(b); err != nil {
		t.Fatal(err)
	}

	got, err := s.ExportBackup()
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Keshkici) != 1 || len(got.Transactions) != 1 || got.Transactions[0].Amount != 100 {
		t.Fatalf("увоз није вратио податке: %+v", got)
	}
	if got.Lists[0].TxID != tx.ID || got.Lists[0].KeshkicID != k.ID || len(got.Lists[0].Items) != 1 || !got.Keshkici[0].IsDefault {
		t.Fatalf("везе између листе, трошка и Кешкића нису очуване: %+v", got.Lists[0])
	}
	if sum, _ := s.MonthTotals("2026-11"); sum.Expenses != 11700 {
		t.Fatalf("очекивано 11700 потрошено, добијено %v", sum.Expenses)
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
