package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDepositLifecycleAndIsolation(t *testing.T) {
	s := openTestStore(t)
	app, _, admin := buildTestApp(t, s)
	login := httptest.NewRequest(http.MethodPost, "/api/login", bytes.NewBufferString(`{"username":"admin","password":"tajna123"}`))
	login.Header.Set("Content-Type", "application/json")
	res, err := app.Test(login)
	if err != nil {
		t.Fatal(err)
	}
	cookie := sessionCookieFrom(res)
	res.Body.Close()
	if cookie == nil {
		t.Fatal("missing session cookie")
	}
	request := func(method, path string, body any, status int) []byte {
		t.Helper()
		payload, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(method, path, bytes.NewReader(payload))
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(cookie)
		res, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		data, _ := io.ReadAll(res.Body)
		if res.StatusCode != status {
			t.Fatalf("%s %s: got %d, want %d: %s", method, path, res.StatusCode, status, data)
		}
		return data
	}
	createK := func(uid int64, title string, amount float64) Keshkic {
		t.Helper()
		k, err := s.CreateKeshkic(uid, Keshkic{Month: "2026-11", Title: title, Amount: amount, Currency: "RSD", Rate: 1, AmountRSD: amount})
		if err != nil {
			t.Fatal(err)
		}
		return k
	}
	k := createK(admin.ID, "Први", 1000)
	second := createK(admin.ID, "Други", 500)
	foreign := createK(admin.ID+1, "Туђи", 3000)
	input := depositInput{KeshkicID: k.ID, Date: "2026-11-09", Amount: 200, Currency: "RSD", Title: "Поклон"}
	var deposit Deposit
	if err := json.Unmarshal(request(http.MethodPost, "/api/deposits", input, http.StatusCreated), &deposit); err != nil {
		t.Fatal(err)
	}
	assertBalance := func(month string, id int64, want float64) {
		t.Helper()
		ks, err := s.ListKeshkici(admin.ID, month)
		if err != nil {
			t.Fatal(err)
		}
		for _, got := range ks {
			if got.ID == id {
				if got.Balance != want {
					t.Fatalf("%s keshkic %d balance: got %v, want %v", month, id, got.Balance, want)
				}
				return
			}
		}
		t.Fatalf("keshkic %d missing", id)
	}
	assertBalance("2026-11", k.ID, 1200)
	assertBalance("2026-12", k.ID, 1200)
	assertBalance("2026-11", second.ID, 500)
	totals, err := s.MonthTotals(admin.ID, "2026-11")
	if err != nil || totals.Deposits != 200 || totals.TotalKeshkic != 1700 || totals.Expenses != 0 {
		t.Fatalf("incorrect totals: %+v, %v", totals, err)
	}
	var month struct {
		Deposits []Deposit `json:"deposits"`
	}
	json.Unmarshal(request(http.MethodGet, fmt.Sprintf("/api/months/2026-11?keshkicId=%d", second.ID), nil, http.StatusOK), &month)
	if len(month.Deposits) != 0 {
		t.Fatal("deposit leaked through keshkic filter")
	}
	json.Unmarshal(request(http.MethodGet, fmt.Sprintf("/api/months/2026-11?keshkicId=%d", k.ID), nil, http.StatusOK), &month)
	if len(month.Deposits) != 1 || month.Deposits[0].ID != deposit.ID {
		t.Fatalf("deposit missing from month response: %+v", month)
	}
	for name, change := range map[string]func(*depositInput){
		"missing keshkic":  func(d *depositInput) { d.KeshkicID = 0 },
		"foreign keshkic":  func(d *depositInput) { d.KeshkicID = foreign.ID },
		"negative amount":  func(d *depositInput) { d.Amount = -1 },
		"rounded to zero":  func(d *depositInput) { d.Amount = 0.001 },
		"invalid date":     func(d *depositInput) { d.Date = "2026-02-30" },
		"before start":     func(d *depositInput) { d.Date = "2026-10-31" },
		"invalid currency": func(d *depositInput) { d.Currency = "GBP" },
	} {
		t.Run(name, func(t *testing.T) {
			invalid := input
			change(&invalid)
			request(http.MethodPost, "/api/deposits", invalid, http.StatusBadRequest)
		})
	}
	// Moving and dating the deposit must undo its old balance contribution.
	input.KeshkicID, input.Date, input.Amount, input.Currency, input.Rate = second.ID, "2026-12-01", 10, "EUR", 117
	path := fmt.Sprintf("/api/deposits/%d", deposit.ID)
	request(http.MethodPut, path, input, http.StatusOK)
	assertBalance("2026-11", k.ID, 1000)
	assertBalance("2026-11", second.ID, 500)
	assertBalance("2026-12", second.ID, 1670)
	if _, err := s.GetDeposit(admin.ID+1, deposit.ID); err != errNotFound {
		t.Fatalf("other user can read deposit: %v", err)
	}
	if err := s.DeleteDeposit(admin.ID+1, deposit.ID); err != errNotFound {
		t.Fatalf("other user can delete deposit: %v", err)
	}
	// Backups must preserve the deposit and remap its envelope for another user.
	backup, err := s.ExportBackup(admin.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := backup.normalize(); err != nil {
		t.Fatal(err)
	}
	if err := s.ImportBackup(admin.ID+1, backup); err != nil {
		t.Fatal(err)
	}
	imported, err := s.ListDeposits(admin.ID+1, "2026-12", 0)
	if err != nil || len(imported) != 1 || imported[0].AmountRSD != 1170 || imported[0].KeshkicID == second.ID {
		t.Fatalf("incorrect imported deposit: %+v, %v", imported, err)
	}
	request(http.MethodDelete, path, nil, http.StatusNoContent)
	assertBalance("2026-12", second.ID, 500)
	request(http.MethodDelete, path, nil, http.StatusNotFound)
}
