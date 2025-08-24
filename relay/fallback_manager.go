package relay

import (
	"claude-code-relay/model"
	"strconv"
	"sync"
	"time"
	"github.com/gin-gonic/gin"
)

// FallbackManager Fallback管理器，负责管理所有的fallback处理器
type FallbackManager struct {
	handlers map[string]*FallbackHandler // key: groupID的字符串表示
	config   *FallbackConfig
	mu       sync.RWMutex
}

// GlobalFallbackManager 全局fallback管理器
var GlobalFallbackManager *FallbackManager

// InitFallbackManager 初始化全局fallback管理器
func InitFallbackManager(config *FallbackConfig) {
	if config == nil {
		config = getDefaultConfig()
	}
	
	GlobalFallbackManager = &FallbackManager{
		handlers: make(map[string]*FallbackHandler),
		config:   config,
	}
}

// GetHandler 获取指定分组的fallback处理器
func (fm *FallbackManager) GetHandler(groupID int) *FallbackHandler {
	groupKey := strconv.Itoa(groupID)
	
	fm.mu.RLock()
	handler, exists := fm.handlers[groupKey]
	fm.mu.RUnlock()
	
	if exists {
		return handler
	}
	
	// 如果不存在，创建新的处理器
	fm.mu.Lock()
	defer fm.mu.Unlock()
	
	// 再次检查，防止并发创建
	if handler, exists := fm.handlers[groupKey]; exists {
		return handler
	}
	
	// 创建新的处理器
	handler = NewFallbackHandler(fm.config)
	fm.handlers[groupKey] = handler
	
	return handler
}

// RemoveHandler 移除指定分组的fallback处理器
func (fm *FallbackManager) RemoveHandler(groupID int) {
	fm.mu.Lock()
	defer fm.mu.Unlock()
	
	groupKey := strconv.Itoa(groupID)
	delete(fm.handlers, groupKey)
}

// GetAllHandlers 获取所有fallback处理器
func (fm *FallbackManager) GetAllHandlers() map[int]*FallbackHandler {
	fm.mu.RLock()
	defer fm.mu.RUnlock()
	
	result := make(map[int]*FallbackHandler)
	for groupKey, handler := range fm.handlers {
		groupID, _ := strconv.Atoi(groupKey)
		result[groupID] = handler
	}
	
	return result
}

// GetStats 获取所有分组的统计信息
func (fm *FallbackManager) GetStats() map[string]interface{} {
	fm.mu.RLock()
	defer fm.mu.RUnlock()
	
	stats := map[string]interface{}{
		"total_groups": len(fm.handlers),
		"groups":       make(map[string]interface{}),
	}
	
	for groupKey, handler := range fm.handlers {
		groupStats := map[string]interface{}{
			"circuit_breaker": handler.circuitBreaker.GetStats(),
			"health_monitor":  handler.healthMonitor.GetAllHealthStats(),
		}
		stats["groups"].(map[string]interface{})[groupKey] = groupStats
	}
	
	return stats
}

// UpdateConfig 更新配置
func (fm *FallbackManager) UpdateConfig(config *FallbackConfig) {
	fm.mu.Lock()
	defer fm.mu.Unlock()
	
	fm.config = config
	
	// 更新所有现有处理器的配置
	for _, handler := range fm.handlers {
		handler.config = config
	}
}

// Cleanup 清理资源
func (fm *FallbackManager) Cleanup() {
	fm.mu.Lock()
	defer fm.mu.Unlock()
	
	// 停止所有健康监控
	for _, handler := range fm.handlers {
		if handler.config.EnableHealthCheck {
			// 这里应该有停止健康监控的逻辑
			// 由于health_monitor使用的是goroutine，需要添加停止机制
		}
	}
	
	// 清空处理器
	fm.handlers = make(map[string]*FallbackHandler)
}

// RequestHandlerFunc 请求处理函数类型 (alias for RequestFunc)
type RequestHandlerFunc = RequestFunc

// HandleWithFallback 使用fallback机制处理请求
func HandleWithFallback(c *gin.Context, accounts []model.Account, requestBody []byte, handlerFunc RequestHandlerFunc) *FallbackResult {
	// 从上下文中获取API Key信息
	apiKey, _ := c.Get("api_key")
	keyInfo := apiKey.(*model.ApiKey)
	
	// 获取对应分组的fallback处理器
	handler := GlobalFallbackManager.GetHandler(keyInfo.GroupID)
	
	// 执行fallback请求
	return handler.HandleRequestWithFallback(c, accounts, requestBody, handlerFunc)
}

// GetFallbackStats 获取指定账号的fallback统计信息
func GetFallbackStats(accountID uint) map[string]interface{} {
	if GlobalFallbackManager == nil {
		return map[string]interface{}{}
	}
	
	// 查找包含该账号的所有分组处理器
	stats := make(map[string]interface{})
	
	for groupID, handler := range GlobalFallbackManager.GetAllHandlers() {
		accountStats := handler.GetAccountStats(accountID)
		if len(accountStats) > 0 {
			stats[strconv.Itoa(groupID)] = accountStats
		}
	}
	
	return stats
}

// UpdateAccountPerformance 更新账号性能数据
func UpdateAccountPerformance(groupID int, accountID uint, success bool, responseTime time.Duration) {
	if GlobalFallbackManager == nil {
		return
	}
	
	handler := GlobalFallbackManager.GetHandler(groupID)
	
	// 如果使用自适应选择器，更新性能数据
	if adaptiveSelector, ok := handler.selector.(*AdaptiveSelector); ok {
		adaptiveSelector.UpdatePerformance(accountID, success, responseTime)
	} else if smartSelector, ok := handler.selector.(*SmartLoadBalanceSelector); ok {
		smartSelector.UpdatePerformance(accountID, success, responseTime)
	}
}

// DisableAccount 禁用指定账号
func DisableAccount(groupID int, accountID uint, duration time.Duration, reason string) {
	if GlobalFallbackManager == nil {
		return
	}
	
	handler := GlobalFallbackManager.GetHandler(groupID)
	handler.healthMonitor.SetAccountDisabled(accountID, duration, reason)
}

// EnableAccount 启用指定账号
func EnableAccount(groupID int, accountID uint) {
	if GlobalFallbackManager == nil {
		return
	}
	
	handler := GlobalFallbackManager.GetHandler(groupID)
	handler.healthMonitor.EnableAccount(accountID)
}

// GetAccountHealth 获取账号健康状态
func GetAccountHealth(groupID int, accountID uint) *AccountHealth {
	if GlobalFallbackManager == nil {
		return nil
	}
	
	handler := GlobalFallbackManager.GetHandler(groupID)
	return handler.healthMonitor.GetAccountHealth(accountID)
}

// GetGroupHealthStats 获取分组健康统计
func GetGroupHealthStats(groupID int) map[uint]*AccountHealth {
	if GlobalFallbackManager == nil {
		return nil
	}
	
	handler := GlobalFallbackManager.GetHandler(groupID)
	return handler.healthMonitor.GetAllHealthStats()
}

// CleanupStaleData 清理过期数据
func CleanupStaleData(maxAge time.Duration) {
	if GlobalFallbackManager == nil {
		return
	}
	
	for _, handler := range GlobalFallbackManager.GetAllHandlers() {
		handler.healthMonitor.CleanupStaleData(maxAge)
	}
}

// FallbackMetrics Fallback指标
type FallbackMetrics struct {
	TotalRequests     int64 `json:"total_requests"`
	SuccessRequests   int64 `json:"success_requests"`
	FailedRequests    int64 `json:"failed_requests"`
	FallbackAttempts  int64 `json:"fallback_attempts"`
	AverageRetries    float64 `json:"average_retries"`
	CircuitBreakerOpens int64 `json:"circuit_breaker_opens"`
}

// GetMetrics 获取全局指标
func GetMetrics() *FallbackMetrics {
	if GlobalFallbackManager == nil {
		return &FallbackMetrics{}
	}
	
	metrics := &FallbackMetrics{}
	
	for _, handler := range GlobalFallbackManager.GetAllHandlers() {
		// 这里可以累积各个处理器的指标
		// 由于当前的实现没有记录这些指标，这里留空
		// 实际使用时可以在FallbackHandler中添加指标收集
		_ = handler // 避免未使用变量警告
	}
	
	return metrics
}

// ResetMetrics 重置指标
func ResetMetrics() {
	if GlobalFallbackManager == nil {
		return
	}
	
	// 重置所有处理器的指标
	// 当前实现中没有具体的指标存储，这里留空
}

// ExportMetrics 导出指标数据
func ExportMetrics() map[string]interface{} {
	if GlobalFallbackManager == nil {
		return map[string]interface{}{}
	}
	
	return map[string]interface{}{
		"fallback_metrics": GetMetrics(),
		"circuit_breakers": GlobalFallbackManager.GetStats(),
		"timestamp":        time.Now(),
	}
}