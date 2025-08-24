package relay

import (
	"claude-code-relay/common"
	"claude-code-relay/model"
	"bytes"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// FallbackStrategy 定义fallback策略
type FallbackStrategy string

const (
	StrategyPriorityFirst FallbackStrategy = "priority_first"  // 优先级优先策略
	StrategyWeighted      FallbackStrategy = "weighted"       // 加权策略
	StrategyRoundRobin    FallbackStrategy = "round_robin"    // 轮询策略
	StrategyLeastUsed     FallbackStrategy = "least_used"     // 最少使用策略
)

// AccountSelector 账号选择器接口
type AccountSelector interface {
	Select(accounts []model.Account) []model.Account
}

// FallbackConfig Fallback配置
type FallbackConfig struct {
	MaxRetries          int               `json:"max_retries"`           // 最大重试次数
	RetryDelay          time.Duration     `json:"retry_delay"`           // 重试延迟
	Strategy            FallbackStrategy  `json:"strategy"`              // 选择策略
	EnableCircuitBreaker bool              `json:"enable_circuit_breaker"` // 启用熔断器
	CircuitBreakerThreshold int            `json:"circuit_breaker_threshold"` // 熔断器阈值
	FailureWindow       time.Duration     `json:"failure_window"`        // 故障窗口时间
	RecoveryWindow      time.Duration     `json:"recovery_window"`       // 恢复窗口时间
	EnableHealthCheck   bool              `json:"enable_health_check"`   // 启用健康检查
	HealthCheckInterval time.Duration     `json:"health_check_interval"` // 健康检查间隔
}

// FallbackResult Fallback结果
type FallbackResult struct {
	Success       bool              `json:"success"`
	Account       *model.Account    `json:"account"`
	StatusCode    int               `json:"status_code"`
	UsageTokens   *common.TokenUsage `json:"usage_tokens,omitempty"`
	ErrorMessage  string            `json:"error_message,omitempty"`
	AttemptCount  int               `json:"attempt_count"`
	Duration      time.Duration     `json:"duration"`
	StrategyUsed  FallbackStrategy  `json:"strategy_used"`
	FailureReason string            `json:"failure_reason,omitempty"`
}

// CircuitBreaker 熔断器
type CircuitBreaker struct {
	mu                sync.RWMutex
	state             int32  // 0:关闭, 1:打开, 2:半开
	failureCount      int64
	lastFailureTime   time.Time
	consecutiveSuccess int64
	threshold         int64
	failureWindow     time.Duration
	recoveryWindow    time.Duration
}

// AccountHealth 账号健康状态
type AccountHealth struct {
	AccountID        uint        `json:"account_id"`
	Status           string      `json:"status"`           // healthy, unhealthy, degraded
	LastCheckTime    time.Time   `json:"last_check_time"`
	SuccessCount     int64       `json:"success_count"`
	FailureCount     int64       `json:"failure_count"`
	AvgResponseTime  time.Duration `json:"avg_response_time"`
	ErrorRate        float64     `json:"error_rate"`
	LastFailure      time.Time   `json:"last_failure"`
	LastSuccess      time.Time   `json:"last_success"`
	DisabledUntil    *time.Time  `json:"disabled_until,omitempty"`
	FailureReason    string      `json:"failure_reason,omitempty"`
}

// HealthMonitor 健康监控器
type HealthMonitor struct {
	mu              sync.RWMutex
	accountHealth   map[uint]*AccountHealth
	checkInterval   time.Duration
	selector        AccountSelector
	config          *FallbackConfig
}

// FallbackHandler Fallback处理器
type FallbackHandler struct {
	config         *FallbackConfig
	selector       AccountSelector
	circuitBreaker *CircuitBreaker
	healthMonitor  *HealthMonitor
	mu             sync.RWMutex
	requestHistory map[uint][]time.Time // 账号请求历史
}

// NewFallbackHandler 创建新的Fallback处理器
func NewFallbackHandler(config *FallbackConfig) *FallbackHandler {
	if config == nil {
		config = getDefaultConfig()
	}

	handler := &FallbackHandler{
		config:         config,
		selector:       createAccountSelector(config.Strategy),
		circuitBreaker: NewCircuitBreaker(config.CircuitBreakerThreshold, config.FailureWindow, config.RecoveryWindow),
		healthMonitor:  NewHealthMonitor(config.HealthCheckInterval, config),
		requestHistory: make(map[uint][]time.Time),
	}

	// 启动健康检查
	if config.EnableHealthCheck {
		go handler.healthMonitor.Start()
	}

	return handler
}

// getDefaultConfig 获取默认配置
func getDefaultConfig() *FallbackConfig {
	config := &FallbackConfig{
		MaxRetries:           3,
		RetryDelay:           0, // 默认无延迟，立即重试
		Strategy:             StrategyPriorityFirst,
		EnableCircuitBreaker: true,
		CircuitBreakerThreshold: 5,
		FailureWindow:       time.Minute * 5,
		RecoveryWindow:      time.Minute * 10,
		EnableHealthCheck:   true,
		HealthCheckInterval: time.Minute * 2,
	}
	
	// 从环境变量读取配置
	if retries := os.Getenv("FALLBACK_MAX_RETRIES"); retries != "" {
		if val, err := strconv.Atoi(retries); err == nil && val > 0 {
			config.MaxRetries = val
		}
	}
	
	if delay := os.Getenv("FALLBACK_RETRY_DELAY"); delay != "" {
		if val, err := time.ParseDuration(delay); err == nil {
			config.RetryDelay = val
		}
	}
	
	if strategy := os.Getenv("FALLBACK_STRATEGY"); strategy != "" {
		config.Strategy = FallbackStrategy(strategy)
	}
	
	if threshold := os.Getenv("FALLBACK_CIRCUIT_BREAKER_THRESHOLD"); threshold != "" {
		if val, err := strconv.Atoi(threshold); err == nil && val > 0 {
			config.CircuitBreakerThreshold = val
		}
	}
	
	if window := os.Getenv("FALLBACK_FAILURE_WINDOW"); window != "" {
		if val, err := time.ParseDuration(window); err == nil {
			config.FailureWindow = val
		}
	}
	
	if window := os.Getenv("FALLBACK_RECOVERY_WINDOW"); window != "" {
		if val, err := time.ParseDuration(window); err == nil {
			config.RecoveryWindow = val
		}
	}
	
	if interval := os.Getenv("FALLBACK_HEALTH_CHECK_INTERVAL"); interval != "" {
		if val, err := time.ParseDuration(interval); err == nil {
			config.HealthCheckInterval = val
		}
	}
	
	if breaker := os.Getenv("FALLBACK_ENABLE_CIRCUIT_BREAKER"); breaker != "" {
		config.EnableCircuitBreaker = breaker == "true" || breaker == "1"
	}
	
	if health := os.Getenv("FALLBACK_ENABLE_HEALTH_CHECK"); health != "" {
		config.EnableHealthCheck = health == "true" || health == "1"
	}
	
	return config
}

// HandleRequestWithFallback 处理带fallback的请求
func (h *FallbackHandler) HandleRequestWithFallback(c *gin.Context, accounts []model.Account, requestBody []byte, requestFunc RequestFunc) *FallbackResult {
	startTime := time.Now()
	
	// 记录请求开始
	log.Printf("🚀 开始处理fallback请求，账号数量: %d，策略: %s", len(accounts), h.config.Strategy)

	// 检查熔断器状态
	if h.config.EnableCircuitBreaker && h.circuitBreaker.IsOpen() {
		return &FallbackResult{
			Success:      false,
			ErrorMessage: "熔断器已开启，暂时停止请求",
			FailureReason: "circuit_breaker_open",
			Duration:     time.Since(startTime),
		}
	}

	// 应用选择策略排序账号
	sortedAccounts := h.selector.Select(accounts)
	if len(sortedAccounts) == 0 {
		return &FallbackResult{
			Success:      false,
			ErrorMessage: "没有可用的账号",
			FailureReason: "no_available_accounts",
			Duration:     time.Since(startTime),
		}
	}

	// 执行fallback逻辑
	result := h.executeFallback(c, sortedAccounts, requestBody, requestFunc, startTime)
	result.StrategyUsed = h.config.Strategy

	// 更新熔断器状态
	if h.config.EnableCircuitBreaker {
		if result.Success {
			h.circuitBreaker.RecordSuccess()
		} else {
			h.circuitBreaker.RecordFailure()
		}
	}

	// 更新健康状态
	if h.config.EnableHealthCheck && result.Account != nil {
		h.healthMonitor.UpdateHealthStatus(result.Account.ID, result)
	}

	return result
}

// executeFallback 执行fallback逻辑
func (h *FallbackHandler) executeFallback(c *gin.Context, accounts []model.Account, requestBody []byte, requestFunc RequestFunc, startTime time.Time) *FallbackResult {
	var lastError string
	var lastResult *FallbackResult

	// 限制最大重试次数
	maxAttempts := min(h.config.MaxRetries, len(accounts))
	
	for i := 0; i < maxAttempts; i++ {
		account := accounts[i]
		attemptStartTime := time.Now()

		log.Printf("🔄 尝试使用账号 [%d/%d]: %s (平台: %s, 优先级: %d)", 
			i+1, maxAttempts, account.Name, account.PlatformType, account.Priority)

		// 检查账号健康状态
		if h.config.EnableHealthCheck {
			if health := h.healthMonitor.GetAccountHealth(account.ID); health != nil {
				if health.Status == "unhealthy" || (health.DisabledUntil != nil && time.Now().Before(*health.DisabledUntil)) {
					log.Printf("⚠️ 跳过不健康的账号: %s, 状态: %s", account.Name, health.Status)
					continue
				}
			}
		}

		// 执行请求
		result := h.executeSingleRequest(c, &account, requestBody, requestFunc, attemptStartTime)
		
		if result.Success {
			log.Printf("✅ 账号 %s 请求成功，耗时: %v，流式输出已完成", account.Name, result.Duration)
			// 成功时，HTTP响应已经通过流式方式实时写入客户端，直接返回结果
			return result
		}

		// 记录失败
		lastError = result.ErrorMessage
		lastResult = result
		
		log.Printf("❌ 账号 %s 请求失败: %s", account.Name, result.ErrorMessage)

		// 如果不是最后一次尝试，添加延迟
		if i < maxAttempts-1 && h.config.RetryDelay > 0 {
			log.Printf("⏳ 等待 %v 后重试...", h.config.RetryDelay)
			time.Sleep(h.config.RetryDelay)
		}
	}

	// 所有账号都失败了，记录失败信息但不写入HTTP响应（由controller处理）
	common.SysError(fmt.Sprintf("所有账号都失败，最后错误: %s", lastError))
	
	if lastResult != nil {
		lastResult.Duration = time.Since(startTime)
		lastResult.FailureReason = "all_accounts_failed"
		return lastResult
	}

	return &FallbackResult{
		Success:       false,
		ErrorMessage:  lastError,
		AttemptCount:  maxAttempts,
		Duration:      time.Since(startTime),
		FailureReason: "all_accounts_failed",
	}
}

// executeSingleRequest 执行单个账号请求（支持真正的流式输出）
func (h *FallbackHandler) executeSingleRequest(c *gin.Context, account *model.Account, requestBody []byte, requestFunc RequestFunc, startTime time.Time) *FallbackResult {
	// 创建流式响应捕获器
	capture := NewStreamingResponseCapture(c.Writer)
	originalWriter := c.Writer
	
	// 临时替换Writer
	c.Writer = capture

	// 执行请求函数
	requestFunc(c, account, requestBody)

	// 恢复原始Writer
	c.Writer = originalWriter

	// 记录请求历史
	h.recordRequest(account.ID)

	// 构建结果
	result := &FallbackResult{
		Account:      account,
		StatusCode:   capture.statusCode,
		AttemptCount: 1,
		Duration:     time.Since(startTime),
		Success:      capture.isSuccess,
	}

	if capture.isSuccess {
		// 成功时流式数据已经直接写入客户端，无需再次处理
		common.SysLog(fmt.Sprintf("✅ 账号 %s 请求成功，状态码: %d，实现真正流式输出", account.Name, capture.statusCode))
	} else {
		// 失败时获取缓存的错误信息
		result.ErrorMessage = string(capture.GetBufferedData())
		common.SysError(fmt.Sprintf("❌ 账号 %s 请求失败，状态码: %d，错误: %s", 
			account.Name, capture.statusCode, result.ErrorMessage))
	}

	return result
}

// recordRequest 记录请求历史
func (h *FallbackHandler) recordRequest(accountID uint) {
	h.mu.Lock()
	defer h.mu.Unlock()
	
	now := time.Now()
	h.requestHistory[accountID] = append(h.requestHistory[accountID], now)
	
	// 清理超过10分钟的记录
	cutoff := now.Add(-time.Minute * 10)
	for id, times := range h.requestHistory {
		var validTimes []time.Time
		for _, t := range times {
			if t.After(cutoff) {
				validTimes = append(validTimes, t)
			}
		}
		h.requestHistory[id] = validTimes
	}
}

// GetAccountStats 获取账号统计信息
func (h *FallbackHandler) GetAccountStats(accountID uint) map[string]interface{} {
	h.mu.RLock()
	defer h.mu.RUnlock()
	
	stats := map[string]interface{}{
		"request_count": len(h.requestHistory[accountID]),
	}
	
	if health := h.healthMonitor.GetAccountHealth(accountID); health != nil {
		stats["health"] = health
	}
	
	return stats
}

// RequestFunc 请求函数类型
type RequestFunc func(c *gin.Context, account *model.Account, requestBody []byte)

// StreamingResponseCapture 流式响应捕获器
type StreamingResponseCapture struct {
	gin.ResponseWriter
	statusCode int
	isSuccess  bool
	buffer     *bytes.Buffer
	headerSet  bool
	headersCopied bool  // 新增：标记是否已复制响应头
}

// NewStreamingResponseCapture 创建流式响应捕获器
func NewStreamingResponseCapture(writer gin.ResponseWriter) *StreamingResponseCapture {
	return &StreamingResponseCapture{
		ResponseWriter: writer,
		statusCode:     200,
		buffer:         bytes.NewBuffer([]byte{}),
		headerSet:      false,
		headersCopied:  false,
	}
}

// Header 拦截Header方法，确保成功时能正确设置流式响应头
func (w *StreamingResponseCapture) Header() http.Header {
	return w.ResponseWriter.Header()
}

// WriteHeader 捕获状态码并判断是否成功
func (w *StreamingResponseCapture) WriteHeader(statusCode int) {
	w.statusCode = statusCode
	w.isSuccess = statusCode >= 200 && statusCode < 400
	
	// 如果是成功响应，立即设置响应头启动流式输出
	if w.isSuccess && !w.headerSet {
		w.ResponseWriter.WriteHeader(statusCode)
		w.headerSet = true
	}
}

// Write 写入响应体
func (w *StreamingResponseCapture) Write(data []byte) (int, error) {
	if len(data) == 0 {
		return 0, nil
	}
	
	if w.isSuccess && w.headerSet {
		// 成功时直接流式写入，实现真正的流式输出
		n, err := w.ResponseWriter.Write(data)
		if err == nil {
			// 立即刷新以确保流式输出
			if flusher, ok := w.ResponseWriter.(interface{ Flush() }); ok {
				flusher.Flush()
			}
		}
		return n, err
	} else if w.isSuccess && !w.headerSet {
		// 第一次成功写入时，先设置响应头
		w.ResponseWriter.WriteHeader(w.statusCode)
		w.headerSet = true
		
		// 然后写入数据
		n, err := w.ResponseWriter.Write(data)
		if err == nil {
			if flusher, ok := w.ResponseWriter.(interface{ Flush() }); ok {
				flusher.Flush()
			}
		}
		return n, err
	} else {
		// 失败时缓存数据，不写入响应
		return w.buffer.Write(data)
	}
}

// GetBufferedData 获取缓存的数据（仅用于失败情况）
func (w *StreamingResponseCapture) GetBufferedData() []byte {
	return w.buffer.Bytes()
}

// min 辅助函数
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}