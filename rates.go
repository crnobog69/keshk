package main

import (
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net/http"
	"sync"
	"time"
)

var supportedCurrencies = []string{"RSD", "EUR", "USD"}

func isSupportedCurrency(c string) bool {
	for _, s := range supportedCurrencies {
		if s == c {
			return true
		}
	}
	return false
}

// RateInfo: Rates[c] = how many RSD for 1 unit of c.
type RateInfo struct {
	Base      string             `json:"base"`
	Rates     map[string]float64 `json:"rates"`
	Source    string             `json:"source"` // live | fallback
	UpdatedAt time.Time          `json:"updatedAt"`
}

// Rates fetches live exchange rates (open.er-api.com, no key needed)
// and caches them; falls back to .env values when offline.
type Rates struct {
	mu        sync.Mutex
	info      RateInfo
	nextFetch time.Time
	fallback  map[string]float64
	client    *http.Client
}

func newRates(fallback map[string]float64) *Rates {
	return &Rates{fallback: fallback, client: &http.Client{Timeout: 5 * time.Second}}
}

func (r *Rates) Get() RateInfo {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.info.Rates == nil || time.Now().After(r.nextFetch) {
		if rates, updated, err := r.fetch(); err != nil {
			log.Printf("⚠️  курсна листа недоступна (%v), користим резервне курсеве", err)
			if r.info.Rates == nil {
				r.info = RateInfo{Base: "RSD", Rates: r.fallback, Source: "fallback", UpdatedAt: time.Now()}
			}
			r.nextFetch = time.Now().Add(10 * time.Minute)
		} else {
			r.info = RateInfo{Base: "RSD", Rates: rates, Source: "live", UpdatedAt: updated}
			r.nextFetch = time.Now().Add(6 * time.Hour)
		}
	}

	cp := make(map[string]float64, len(r.info.Rates))
	for k, v := range r.info.Rates {
		cp[k] = v
	}
	out := r.info
	out.Rates = cp
	return out
}

func (r *Rates) Rate(currency string) float64 {
	if currency == "RSD" {
		return 1
	}
	if v, ok := r.Get().Rates[currency]; ok && v > 0 {
		return v
	}
	return r.fallback[currency]
}

func (r *Rates) fetch() (map[string]float64, time.Time, error) {
	resp, err := r.client.Get("https://open.er-api.com/v6/latest/RSD")
	if err != nil {
		return nil, time.Time{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, time.Time{}, fmt.Errorf("status %d", resp.StatusCode)
	}

	var body struct {
		Result string             `json:"result"`
		Unix   int64              `json:"time_last_update_unix"`
		Rates  map[string]float64 `json:"rates"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, time.Time{}, err
	}
	if body.Result != "success" {
		return nil, time.Time{}, fmt.Errorf("result=%s", body.Result)
	}

	out := map[string]float64{"RSD": 1}
	for _, c := range supportedCurrencies {
		if c == "RSD" {
			continue
		}
		v := body.Rates[c]
		if v <= 0 {
			out[c] = r.fallback[c]
			continue
		}
		out[c] = math.Round((1/v)*10000) / 10000 // invert: RSD per 1 unit
	}
	return out, time.Unix(body.Unix, 0), nil
}
