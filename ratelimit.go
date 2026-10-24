package main

import (
	"net"
	"strings"
	"sync"
	"time"

	"github.com/gofiber/fiber/v3"
)

const (
	loginMaxFails   = 5                // неуспешних покушаја по IP адреси
	loginGlobalMax  = 50               // неуспешних покушаја укупно (заштита од distributed напада)
	loginWindow     = 15 * time.Minute // прозор бројања и трајање блокаде
)

type failEntry struct {
	count int
	first time.Time
}

// LoginLimiter blocks an IP after too many failed logins within loginWindow.
type LoginLimiter struct {
	mu     sync.Mutex
	byIP   map[string]*failEntry
	global failEntry
}

func newLoginLimiter() *LoginLimiter {
	return &LoginLimiter{byIP: map[string]*failEntry{}}
}

// clientIP returns the real client IP. Behind a local Cloudflare Tunnel the
// TCP peer is 127.0.0.1, so Cf-Connecting-Ip is trusted ONLY for loopback peers.
func clientIP(c fiber.Ctx) string {
	ip := c.IP()
	if p := net.ParseIP(ip); p != nil && p.IsLoopback() {
		if cf := strings.TrimSpace(c.Get("Cf-Connecting-Ip")); cf != "" {
			return cf
		}
	}
	return ip
}

func (e *failEntry) active(now time.Time) bool {
	return e.count > 0 && now.Sub(e.first) < loginWindow
}

// blocked reports whether ip may not attempt a login, and how long to wait.
func (l *LoginLimiter) blocked(ip string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	// чишћење старих уноса
	for k, e := range l.byIP {
		if !e.active(now) {
			delete(l.byIP, k)
		}
	}
	if l.global.active(now) && l.global.count >= loginGlobalMax {
		return true, loginWindow - now.Sub(l.global.first)
	}
	if e, ok := l.byIP[ip]; ok && e.count >= loginMaxFails {
		return true, loginWindow - now.Sub(e.first)
	}
	return false, 0
}

func (l *LoginLimiter) fail(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	e, ok := l.byIP[ip]
	if !ok || !e.active(now) {
		e = &failEntry{first: now}
		l.byIP[ip] = e
	}
	e.count++
	if !l.global.active(now) {
		l.global = failEntry{first: now}
	}
	l.global.count++
}

func (l *LoginLimiter) success(ip string) {
	l.mu.Lock()
	delete(l.byIP, ip)
	l.mu.Unlock()
}
