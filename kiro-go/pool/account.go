// Package pool 账号池管理
// 实现随机负载均衡、错误冷却、Token 刷新
package pool

import (
	"kiro-api-proxy/config"
	"sync"
	"time"
)

type breakerEvent struct {
	at      time.Time
	success bool
}

type breakerRuntimeState struct {
	State         string
	OpenedAt      time.Time
	OpenUntil     time.Time
	FailureCount  int
	SuccessStreak int
	Manual        bool
	Events        []breakerEvent
}

// BreakerAccountStatus is the admin/UI snapshot for an account circuit.
type BreakerAccountStatus struct {
	ID               string  `json:"id"`
	Email            string  `json:"email,omitempty"`
	Enabled          bool    `json:"enabled"`
	State            string  `json:"state"`
	Manual           bool    `json:"manual"`
	FailureCount     int     `json:"failureCount"`
	SuccessStreak    int     `json:"successStreak"`
	RecentRequests   int     `json:"recentRequests"`
	RecentFailures   int     `json:"recentFailures"`
	ErrorRate        float64 `json:"errorRate"`
	OpenUntil        int64   `json:"openUntil,omitempty"`
	CooldownUntil    int64   `json:"cooldownUntil,omitempty"`
	RequestCount     int     `json:"requestCount"`
	ErrorCount       int     `json:"errorCount"`
	UsageCurrent     float64 `json:"usageCurrent,omitempty"`
	UsageLimit       float64 `json:"usageLimit,omitempty"`
	UsagePercent     float64 `json:"usagePercent,omitempty"`
	SubscriptionType string  `json:"subscriptionType,omitempty"`
	ProxyNode        string  `json:"proxyNode,omitempty"`
	ProxyURL         string  `json:"proxyUrl,omitempty"`
}

// AccountPool 账号池
type AccountPool struct {
	mu            sync.RWMutex
	accounts      []config.Account
	cooldowns     map[string]time.Time // 账号冷却时间
	errorCounts   map[string]int       // 连续错误计数
	breakerStates map[string]*breakerRuntimeState
	weightCurrent map[string]int
}

var (
	pool     *AccountPool
	poolOnce sync.Once
)

// GetPool 获取全局账号池单例
func GetPool() *AccountPool {
	poolOnce.Do(func() {
		pool = &AccountPool{
			cooldowns:     make(map[string]time.Time),
			errorCounts:   make(map[string]int),
			breakerStates: make(map[string]*breakerRuntimeState),
			weightCurrent: make(map[string]int),
		}
		pool.Reload()
	})
	return pool
}

// Reload 从配置重新加载账号
func (p *AccountPool) Reload() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.accounts = config.GetEnabledAccounts()
	known := make(map[string]bool, len(p.accounts))
	for _, acc := range p.accounts {
		known[acc.ID] = true
	}
	for id := range p.breakerStates {
		if !known[id] {
			delete(p.breakerStates, id)
		}
	}
	for id := range p.weightCurrent {
		if !known[id] {
			delete(p.weightCurrent, id)
		}
	}
}

// GetNext 获取一个可用账号：在可用账号中按权重做平滑轮询。
func (p *AccountPool) GetNext() *config.Account {
	p.mu.Lock()
	defer p.mu.Unlock()

	if len(p.accounts) == 0 {
		return nil
	}

	now := time.Now()
	breakerCfg := config.GetCircuitBreakerConfig()
	candidates := make([]*config.Account, 0, len(p.accounts))

	for i := range p.accounts {
		acc := &p.accounts[i]

		if breakerCfg.Enabled && p.isCircuitOpenLocked(acc.ID, now, breakerCfg) {
			continue
		}

		// 跳过冷却中的账号
		if cooldown, ok := p.cooldowns[acc.ID]; ok && now.Before(cooldown) {
			continue
		}

		// 跳过即将过期的 Token
		if acc.ExpiresAt > 0 && now.Unix() > acc.ExpiresAt-300 {
			continue
		}

		candidates = append(candidates, acc)
	}

	picked := p.pickSmoothWeightedLocked(candidates)
	if picked != nil {
		return picked
	}

	// 无可用账号，返回冷却时间最短的
	var best *config.Account
	var earliest time.Time
	for i := range p.accounts {
		acc := &p.accounts[i]
		if breakerCfg.Enabled && p.isCircuitOpenLocked(acc.ID, now, breakerCfg) {
			continue
		}
		if cooldown, ok := p.cooldowns[acc.ID]; ok {
			if best == nil || cooldown.Before(earliest) {
				best = acc
				earliest = cooldown
			}
		} else {
			return acc
		}
	}
	return best
}

func (p *AccountPool) pickSmoothWeightedLocked(candidates []*config.Account) *config.Account {
	if len(candidates) == 0 {
		return nil
	}
	totalWeight := 0
	var best *config.Account
	bestScore := 0

	for _, a := range candidates {
		w := a.Weight
		if w <= 0 {
			w = 100
		}
		totalWeight += w

		score := p.weightCurrent[a.ID] + w
		p.weightCurrent[a.ID] = score
		if best == nil || score > bestScore {
			best = a
			bestScore = score
		}
	}

	if best != nil {
		p.weightCurrent[best.ID] -= totalWeight
	}
	return best
}

// GetByID 根据 ID 获取账号
func (p *AccountPool) GetByID(id string) *config.Account {
	p.mu.RLock()
	defer p.mu.RUnlock()
	for i := range p.accounts {
		if p.accounts[i].ID == id {
			return &p.accounts[i]
		}
	}
	return nil
}

// RecordSuccess 记录请求成功，清除冷却
func (p *AccountPool) RecordSuccess(id string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.cooldowns, id)
	p.errorCounts[id] = 0
	p.recordBreakerSuccessLocked(id, time.Now(), config.GetCircuitBreakerConfig())
}

// RecordError 记录请求错误，设置冷却
func (p *AccountPool) RecordError(id string, isQuotaError bool) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.errorCounts[id]++
	now := time.Now()
	breakerCfg := config.GetCircuitBreakerConfig()
	p.recordBreakerErrorLocked(id, now, isQuotaError, breakerCfg)

	if isQuotaError {
		// 配额错误，冷却 1 小时
		p.cooldowns[id] = now.Add(time.Hour)
	} else if p.errorCounts[id] >= 3 {
		// 连续 3 次错误，冷却 1 分钟
		p.cooldowns[id] = now.Add(time.Minute)
	}
}

// UpdateToken 更新账号 Token
func (p *AccountPool) UpdateToken(id, accessToken, refreshToken string, expiresAt int64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for i := range p.accounts {
		if p.accounts[i].ID == id {
			p.accounts[i].AccessToken = accessToken
			if refreshToken != "" {
				p.accounts[i].RefreshToken = refreshToken
			}
			p.accounts[i].ExpiresAt = expiresAt
			break
		}
	}
}

// Count 返回账号总数
func (p *AccountPool) Count() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return len(p.accounts)
}

// AvailableCount 返回可用账号数
func (p *AccountPool) AvailableCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := time.Now()
	breakerCfg := config.GetCircuitBreakerConfig()
	count := 0
	for _, acc := range p.accounts {
		if breakerCfg.Enabled && p.isCircuitOpenLocked(acc.ID, now, breakerCfg) {
			continue
		}
		if cooldown, ok := p.cooldowns[acc.ID]; ok && now.Before(cooldown) {
			continue
		}
		count++
	}
	return count
}

// UpdateStats 更新账号统计
func (p *AccountPool) UpdateStats(id string, tokens int, credits float64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for i := range p.accounts {
		if p.accounts[i].ID == id {
			p.accounts[i].RequestCount++
			p.accounts[i].TotalTokens += tokens
			p.accounts[i].TotalCredits += credits
			p.accounts[i].LastUsed = time.Now().Unix()
			go config.UpdateAccountStats(id, p.accounts[i].RequestCount, p.accounts[i].ErrorCount, p.accounts[i].TotalTokens, p.accounts[i].TotalCredits, p.accounts[i].LastUsed)
			break
		}
	}
}

// GetAllAccounts 获取所有账号副本
func (p *AccountPool) GetAllAccounts() []config.Account {
	p.mu.RLock()
	defer p.mu.RUnlock()
	result := make([]config.Account, len(p.accounts))
	copy(result, p.accounts)
	return result
}

func (p *AccountPool) breakerStateLocked(id string) *breakerRuntimeState {
	st := p.breakerStates[id]
	if st == nil {
		st = &breakerRuntimeState{State: "closed"}
		p.breakerStates[id] = st
	}
	if st.State == "" {
		st.State = "closed"
	}
	return st
}

func (p *AccountPool) isCircuitOpenLocked(id string, now time.Time, cfg config.CircuitBreakerConfig) bool {
	st := p.breakerStateLocked(id)
	if st.State != "open" {
		return false
	}
	if st.OpenUntil.IsZero() || now.Before(st.OpenUntil) {
		return true
	}
	st.State = "half_open"
	st.Manual = false
	st.SuccessStreak = 0
	return false
}

func (p *AccountPool) recordBreakerSuccessLocked(id string, now time.Time, cfg config.CircuitBreakerConfig) {
	st := p.breakerStateLocked(id)
	st.Events = appendBreakerEvent(st.Events, breakerEvent{at: now, success: true}, now, cfg.FailureWindowSec)
	if !cfg.Enabled {
		return
	}
	if st.State == "half_open" {
		st.SuccessStreak++
		if st.SuccessStreak >= cfg.HalfOpenMaxSuccess {
			st.State = "closed"
			st.Manual = false
			st.OpenUntil = time.Time{}
			st.OpenedAt = time.Time{}
			st.FailureCount = 0
			st.SuccessStreak = 0
		}
		return
	}
	if st.State != "open" {
		st.State = "closed"
		st.Manual = false
		st.FailureCount = 0
		st.SuccessStreak = 0
	}
}

func (p *AccountPool) recordBreakerErrorLocked(id string, now time.Time, isQuotaError bool, cfg config.CircuitBreakerConfig) {
	st := p.breakerStateLocked(id)
	st.Events = appendBreakerEvent(st.Events, breakerEvent{at: now, success: false}, now, cfg.FailureWindowSec)
	st.FailureCount++
	if !cfg.Enabled {
		return
	}
	if isQuotaError {
		p.openCircuitLocked(st, now, time.Duration(cfg.OpenDurationSec)*time.Second, false)
		return
	}
	if st.State == "half_open" {
		p.openCircuitLocked(st, now, time.Duration(cfg.OpenDurationSec)*time.Second, false)
		return
	}
	recent, failures := countBreakerEvents(st.Events)
	errorRate := 0.0
	if recent > 0 {
		errorRate = float64(failures) / float64(recent)
	}
	if st.FailureCount >= cfg.FailureThreshold || (recent >= cfg.ErrorRateMinReqs && errorRate >= cfg.ErrorRateThreshold) {
		p.openCircuitLocked(st, now, time.Duration(cfg.OpenDurationSec)*time.Second, false)
	}
}

func (p *AccountPool) openCircuitLocked(st *breakerRuntimeState, now time.Time, d time.Duration, manual bool) {
	if d <= 0 {
		d = time.Duration(config.DefaultCircuitBreakerConfig().OpenDurationSec) * time.Second
	}
	st.State = "open"
	st.OpenedAt = now
	st.OpenUntil = now.Add(d)
	st.SuccessStreak = 0
	st.Manual = manual
}

func appendBreakerEvent(events []breakerEvent, ev breakerEvent, now time.Time, windowSec int) []breakerEvent {
	if windowSec <= 0 {
		windowSec = config.DefaultCircuitBreakerConfig().FailureWindowSec
	}
	cutoff := now.Add(-time.Duration(windowSec) * time.Second)
	kept := events[:0]
	for _, item := range events {
		if item.at.After(cutoff) {
			kept = append(kept, item)
		}
	}
	kept = append(kept, ev)
	return kept
}

func countBreakerEvents(events []breakerEvent) (int, int) {
	failures := 0
	for _, ev := range events {
		if !ev.success {
			failures++
		}
	}
	return len(events), failures
}

// ForceOpenCircuit manually opens an account circuit for the requested duration.
func (p *AccountPool) ForceOpenCircuit(id string, durationSec int) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !accountExists(id) {
		return false
	}
	if durationSec <= 0 {
		durationSec = config.GetCircuitBreakerConfig().OpenDurationSec
	}
	st := p.breakerStateLocked(id)
	p.openCircuitLocked(st, time.Now(), time.Duration(durationSec)*time.Second, true)
	return true
}

// CloseCircuit resets an account circuit and short cooldown.
func (p *AccountPool) CloseCircuit(id string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !accountExists(id) {
		return false
	}
	st := p.breakerStateLocked(id)
	st.State = "closed"
	st.Manual = false
	st.OpenUntil = time.Time{}
	st.OpenedAt = time.Time{}
	st.FailureCount = 0
	st.SuccessStreak = 0
	st.Events = nil
	p.errorCounts[id] = 0
	delete(p.cooldowns, id)
	return true
}

func (p *AccountPool) GetByIDLocked(id string) *config.Account {
	for i := range p.accounts {
		if p.accounts[i].ID == id {
			return &p.accounts[i]
		}
	}
	return nil
}

func accountExists(id string) bool {
	for _, acc := range config.GetAccounts() {
		if acc.ID == id {
			return true
		}
	}
	return false
}

// BreakerSnapshot returns current breaker and cooldown state for all accounts.
func (p *AccountPool) BreakerSnapshot(allAccounts []config.Account) []BreakerAccountStatus {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := time.Now()
	cfg := config.GetCircuitBreakerConfig()
	result := make([]BreakerAccountStatus, 0, len(allAccounts))
	for _, acc := range allAccounts {
		st := p.breakerStateLocked(acc.ID)
		if cfg.Enabled && st.State == "open" && !st.OpenUntil.IsZero() && now.After(st.OpenUntil) {
			st.State = "half_open"
			st.Manual = false
			st.SuccessStreak = 0
		}
		st.Events = appendBreakerEvent(st.Events, breakerEvent{at: now, success: true}, now, cfg.FailureWindowSec)
		if len(st.Events) > 0 {
			st.Events = st.Events[:len(st.Events)-1]
		}
		recent, failures := countBreakerEvents(st.Events)
		errorRate := 0.0
		if recent > 0 {
			errorRate = float64(failures) / float64(recent)
		}
		status := BreakerAccountStatus{
			ID:               acc.ID,
			Email:            acc.Email,
			Enabled:          acc.Enabled,
			State:            st.State,
			Manual:           st.Manual,
			FailureCount:     st.FailureCount,
			SuccessStreak:    st.SuccessStreak,
			RecentRequests:   recent,
			RecentFailures:   failures,
			ErrorRate:        errorRate,
			RequestCount:     acc.RequestCount,
			ErrorCount:       acc.ErrorCount,
			UsageCurrent:     acc.UsageCurrent,
			UsageLimit:       acc.UsageLimit,
			UsagePercent:     acc.UsagePercent,
			SubscriptionType: acc.SubscriptionType,
			ProxyNode:        acc.ProxyNode,
			ProxyURL:         acc.ProxyURL,
		}
		if st.State == "open" && !st.OpenUntil.IsZero() {
			status.OpenUntil = st.OpenUntil.Unix()
		}
		if cooldown, ok := p.cooldowns[acc.ID]; ok && now.Before(cooldown) {
			status.CooldownUntil = cooldown.Unix()
		}
		result = append(result, status)
	}
	return result
}
