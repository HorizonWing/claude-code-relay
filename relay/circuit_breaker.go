package relay

import (
	"claude-code-relay/model"
	"log"
	"sync/atomic"
	"time"
)

// CircuitBreakerState 熔断器状态
type CircuitBreakerState int32

const (
	CircuitClosed   CircuitBreakerState = 0 // 熔断器关闭（正常）
	CircuitOpen     CircuitBreakerState = 1 // 熔断器开启（熔断中）
	CircuitHalfOpen CircuitBreakerState = 2 // 熔断器半开（探测恢复）
)

// NewCircuitBreaker 创建新的熔断器
func NewCircuitBreaker(threshold int, failureWindow, recoveryWindow time.Duration) *CircuitBreaker {
	return &CircuitBreaker{
		threshold:      int64(threshold),
		failureWindow:  failureWindow,
		recoveryWindow: recoveryWindow,
	}
}

// IsOpen 检查熔断器是否开启
func (cb *CircuitBreaker) IsOpen() bool {
	state := atomic.LoadInt32(&cb.state)
	
	switch CircuitBreakerState(state) {
	case CircuitClosed:
		return false
	case CircuitOpen:
		// 检查是否可以转为半开状态
		if time.Since(cb.lastFailureTime) > cb.recoveryWindow {
			if atomic.CompareAndSwapInt32(&cb.state, int32(CircuitOpen), int32(CircuitHalfOpen)) {
				cb.consecutiveSuccess = 0
				return false
			}
		}
		return true
	case CircuitHalfOpen:
		return false
	default:
		return false
	}
}

// RecordSuccess 记录成功
func (cb *CircuitBreaker) RecordSuccess() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	
	state := atomic.LoadInt32(&cb.state)
	
	if CircuitBreakerState(state) == CircuitHalfOpen {
		cb.consecutiveSuccess++
		// 如果连续成功次数达到阈值，转为关闭状态
		if cb.consecutiveSuccess >= 5 {
			atomic.StoreInt32(&cb.state, int32(CircuitClosed))
			cb.failureCount = 0
		}
	} else if CircuitBreakerState(state) == CircuitClosed {
		// 在关闭状态下重置失败计数
		if time.Since(cb.lastFailureTime) > cb.failureWindow {
			cb.failureCount = 0
		}
	}
}

// RecordFailure 记录失败
func (cb *CircuitBreaker) RecordFailure() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	
	cb.failureCount++
	cb.lastFailureTime = time.Now()
	
	state := atomic.LoadInt32(&cb.state)
	
	if CircuitBreakerState(state) == CircuitHalfOpen {
		// 半开状态下失败，直接转为开启状态
		atomic.StoreInt32(&cb.state, int32(CircuitOpen))
	} else if CircuitBreakerState(state) == CircuitClosed {
		// 关闭状态下检查是否需要开启熔断器
		if cb.failureCount >= cb.threshold {
			atomic.StoreInt32(&cb.state, int32(CircuitOpen))
		}
	}
}

// GetState 获取熔断器状态
func (cb *CircuitBreaker) GetState() CircuitBreakerState {
	return CircuitBreakerState(atomic.LoadInt32(&cb.state))
}

// GetStats 获取熔断器统计信息
func (cb *CircuitBreaker) GetStats() map[string]interface{} {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	
	return map[string]interface{}{
		"state":             cb.GetState(),
		"failure_count":     cb.failureCount,
		"last_failure_time": cb.lastFailureTime,
		"consecutive_success": cb.consecutiveSuccess,
		"threshold":         cb.threshold,
	}
}

// NewHealthMonitor 创建健康监控器
func NewHealthMonitor(checkInterval time.Duration, config *FallbackConfig) *HealthMonitor {
	return &HealthMonitor{
		accountHealth: make(map[uint]*AccountHealth),
		checkInterval: checkInterval,
		config:        config,
	}
}

// Start 启动健康监控
func (hm *HealthMonitor) Start() {
	ticker := time.NewTicker(hm.checkInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			hm.performHealthCheck()
		}
	}
}

// performHealthCheck 执行健康检查
func (hm *HealthMonitor) performHealthCheck() {
	hm.mu.Lock()
	defer hm.mu.Unlock()

	for accountID, health := range hm.accountHealth {
		// 更新健康状态
		health.LastCheckTime = time.Now()
		
		// 计算错误率
		totalRequests := health.SuccessCount + health.FailureCount
		if totalRequests > 0 {
			health.ErrorRate = float64(health.FailureCount) / float64(totalRequests)
		}

		// 更新健康状态
		previousStatus := health.Status
		health.Status = hm.determineHealthStatus(health)

		// 如果状态发生变化，记录日志
		if previousStatus != health.Status {
			account, _ := model.GetAccountByID(accountID)
			if account != nil {
				log.Printf("🏥 账号 %s 健康状态变化: %s -> %s", account.Name, previousStatus, health.Status)
			}
		}
	}
}

// determineHealthStatus 确定健康状态
func (hm *HealthMonitor) determineHealthStatus(health *AccountHealth) string {
	now := time.Now()
	
	// 如果账号被临时禁用
	if health.DisabledUntil != nil && now.Before(*health.DisabledUntil) {
		return "disabled"
	}

	// 如果最近5分钟内没有任何请求
	if health.LastCheckTime.Sub(health.LastSuccess) > time.Minute*5 && 
	   health.LastCheckTime.Sub(health.LastFailure) > time.Minute*5 {
		return "idle"
	}

	// 根据错误率判断
	if health.ErrorRate > 0.5 {
		return "unhealthy"
	} else if health.ErrorRate > 0.2 {
		return "degraded"
	}

	// 根据响应时间判断
	if health.AvgResponseTime > time.Second*30 {
		return "degraded"
	} else if health.AvgResponseTime > time.Minute {
		return "unhealthy"
	}

	return "healthy"
}

// UpdateHealthStatus 更新账号健康状态
func (hm *HealthMonitor) UpdateHealthStatus(accountID uint, result *FallbackResult) {
	hm.mu.Lock()
	defer hm.mu.Unlock()

	health := hm.accountHealth[accountID]
	if health == nil {
		health = &AccountHealth{
			AccountID:     accountID,
			Status:        "healthy",
			LastCheckTime: time.Now(),
		}
		hm.accountHealth[accountID] = health
	}

	// 更新统计信息
	if result.Success {
		health.SuccessCount++
		health.LastSuccess = time.Now()
	} else {
		health.FailureCount++
		health.LastFailure = time.Now()
		health.FailureReason = result.ErrorMessage
		
		// 如果连续失败，考虑临时禁用
		if hm.shouldTemporarilyDisable(health) {
			disabledUntil := time.Now().Add(time.Minute * 10)
			health.DisabledUntil = &disabledUntil
			log.Printf("⚠️ 账号 %d 因连续失败被临时禁用至 %s", accountID, disabledUntil.Format(time.RFC3339))
		}
	}

	// 更新平均响应时间
	if health.AvgResponseTime == 0 {
		health.AvgResponseTime = result.Duration
	} else {
		health.AvgResponseTime = (health.AvgResponseTime + result.Duration) / 2
	}

	// 重新计算错误率
	totalRequests := health.SuccessCount + health.FailureCount
	if totalRequests > 0 {
		health.ErrorRate = float64(health.FailureCount) / float64(totalRequests)
	}
}

// shouldTemporarilyDisable 判断是否应该临时禁用账号
func (hm *HealthMonitor) shouldTemporarilyDisable(health *AccountHealth) bool {
	// 如果错误率超过80%且最近10次请求中有8次失败
	if health.ErrorRate > 0.8 {
		// 检查最近的失败时间
		recentFailures := 0
		now := time.Now()
		cutoff := now.Add(-time.Minute * 5)
		
		if health.LastFailure.After(cutoff) {
			recentFailures++
		}
		
		return recentFailures >= 3
	}
	
	return false
}

// GetAccountHealth 获取账号健康状态
func (hm *HealthMonitor) GetAccountHealth(accountID uint) *AccountHealth {
	hm.mu.RLock()
	defer hm.mu.RUnlock()
	
	return hm.accountHealth[accountID]
}

// GetAllHealthStats 获取所有账号的健康状态
func (hm *HealthMonitor) GetAllHealthStats() map[uint]*AccountHealth {
	hm.mu.RLock()
	defer hm.mu.RUnlock()
	
	// 创建副本返回
	result := make(map[uint]*AccountHealth)
	for id, health := range hm.accountHealth {
		healthCopy := *health
		result[id] = &healthCopy
	}
	
	return result
}

// CleanupStaleData 清理过期数据
func (hm *HealthMonitor) CleanupStaleData(maxAge time.Duration) {
	hm.mu.Lock()
	defer hm.mu.Unlock()
	
	cutoff := time.Now().Add(-maxAge)
	for accountID, health := range hm.accountHealth {
		// 如果账号长时间没有活动，清理其数据
		if health.LastCheckTime.Before(cutoff) {
			delete(hm.accountHealth, accountID)
		}
	}
}

// SetAccountDisabled 手动设置账号禁用状态
func (hm *HealthMonitor) SetAccountDisabled(accountID uint, duration time.Duration, reason string) {
	hm.mu.Lock()
	defer hm.mu.Unlock()
	
	health := hm.accountHealth[accountID]
	if health == nil {
		health = &AccountHealth{
			AccountID:     accountID,
			Status:        "disabled",
			LastCheckTime: time.Now(),
		}
		hm.accountHealth[accountID] = health
	}
	
	disabledUntil := time.Now().Add(duration)
	health.DisabledUntil = &disabledUntil
	health.Status = "disabled"
	health.FailureReason = reason
	
	log.Printf("🚫 账号 %d 被手动禁用至 %s，原因: %s", accountID, disabledUntil.Format(time.RFC3339), reason)
}

// EnableAccount 手动启用账号
func (hm *HealthMonitor) EnableAccount(accountID uint) {
	hm.mu.Lock()
	defer hm.mu.Unlock()
	
	health := hm.accountHealth[accountID]
	if health != nil {
		health.DisabledUntil = nil
		health.Status = "healthy"
		health.FailureReason = ""
		log.Printf("✅ 账号 %d 被手动启用", accountID)
	}
}