package main

import (
	"testing"
	"time"
)

func TestSeedDemo(t *testing.T) {
	// 31. март: претходни месеци су краћи, а први дан у месецу даје клампиране датуме.
	for _, now := range []time.Time{
		time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC),
		time.Date(2026, 3, 31, 12, 0, 0, 0, time.UTC),
		time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC),
	} {
		t.Run(now.Format("2006-01-02"), func(t *testing.T) {
			s := openTestStore(t)
			if err := seedDemo(s, 1, now); err != nil {
				t.Fatalf("%v: %v", now, err)
			}
			b, err := s.ExportBackup(1)
			if err != nil {
				t.Fatal(err)
			}
			if len(b.Keshkici) != 4 || len(b.Lists) != 3 || len(b.Transactions) < 20 {
				t.Fatalf("неочекиван садржај: %d/%d/%d", len(b.Keshkici), len(b.Transactions), len(b.Lists))
			}
			if err := b.normalize(); err != nil {
				t.Fatalf("пробни подаци нису исправни: %v", err)
			}
			// Поновно пуњење не сме ништа да дода.
			if err := seedDemo(s, 1, now); err != nil {
				t.Fatal(err)
			}
			again, _ := s.ExportBackup(1)
			if len(again.Transactions) != len(b.Transactions) {
				t.Fatal("пробни подаци су додати у непразну базу")
			}
		})
	}
}
