package main

import (
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gofiber/fiber/v3"
)

type failEntry struct {
	count int
	first time.Time
}

// RateLimiter блокира IP адресу (или глобално све захтеве) након превише неуспешних
// покушаја у задатом временском прозору. Користи се и за пријаву и за код регистрације.
type RateLimiter struct {
	mu        sync.Mutex
	maxPerIP  int
	maxGlobal int
	window    time.Duration
	byIP      map[string]*failEntry
	global    failEntry
}

func newRateLimiter(maxPerIP, maxGlobal int, window time.Duration) *RateLimiter {
	return &RateLimiter{
		maxPerIP:  maxPerIP,
		maxGlobal: maxGlobal,
		window:    window,
		byIP:      map[string]*failEntry{},
	}
}

func newLoginLimiter() *RateLimiter {
	return newRateLimiter(5, 50, 15*time.Minute)
}

func newInviteLimiter() *RateLimiter {
	return newRateLimiter(5, 30, 15*time.Minute)
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

func (e *failEntry) active(now time.Time, window time.Duration) bool {
	return e.count > 0 && now.Sub(e.first) < window
}

// blocked reports whether ip may not attempt an action, and how long to wait.
func (l *RateLimiter) blocked(ip string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	for k, e := range l.byIP {
		if !e.active(now, l.window) {
			delete(l.byIP, k)
		}
	}
	if l.global.active(now, l.window) && l.global.count >= l.maxGlobal {
		return true, l.window - now.Sub(l.global.first)
	}
	if e, ok := l.byIP[ip]; ok && e.count >= l.maxPerIP {
		return true, l.window - now.Sub(e.first)
	}
	return false, 0
}

func (l *RateLimiter) fail(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	e, ok := l.byIP[ip]
	if !ok || !e.active(now, l.window) {
		e = &failEntry{first: now}
		l.byIP[ip] = e
	}
	e.count++
	if !l.global.active(now, l.window) {
		l.global = failEntry{first: now}
	}
	l.global.count++
}

func (l *RateLimiter) success(ip string) {
	l.mu.Lock()
	delete(l.byIP, ip)
	l.mu.Unlock()
}

func rejectRateLimited(c fiber.Ctx, wait time.Duration) error {
	c.Set("Retry-After", strconv.Itoa(int(wait.Seconds())+1))
	return fail(c, fiber.StatusTooManyRequests, "Превише неуспешних покушаја. Покушај поново за "+strconv.Itoa(int(wait.Minutes())+1)+" мин.")
}
