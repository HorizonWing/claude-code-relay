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
	"strings"
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
	stopChan        chan struct{} // 停止信号通道
	ticker          *time.Ticker  // 定时器
}

// FallbackHandler Fallback处理器
type FallbackHandler struct {
	config         *FallbackConfig
	selector       AccountSelector
	circuitBreaker *CircuitBreaker
	healthMonitor  *HealthMonitor
	mu             sync.RWMutex
	requestHistory map[uint][]time.Time // 账号请求历史
	stopChan       chan struct{}        // 停止信号通道
	cleanupTicker  *time.Ticker         // 清理定时器
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
		stopChan:       make(chan struct{}),
	}

	// 启动健康检查
	if config.EnableHealthCheck {
		go handler.healthMonitor.Start()
	}

	// 启动定期清理任务
	handler.startCleanupTask()

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
			// 记录成功的日志，TTFB信息已经在executeSingleRequest中记录
			log.Printf("✅ 账号 %s fallback请求成功，总耗时: %v，流式输出已完成", account.Name, result.Duration)
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

// executeSingleRequest 执行单个账号请求（支持流式和非流式模式）
func (h *FallbackHandler) executeSingleRequest(c *gin.Context, account *model.Account, requestBody []byte, requestFunc RequestFunc, startTime time.Time) *FallbackResult {
	// 创建自适应响应捕获器，传入请求开始时间和请求体（用于检测流式模式）
	capture := NewStreamingResponseCapture(c.Writer, startTime, requestBody)
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
		// 如果是非流式模式，需要输出缓存的数据
		if !capture.isStreamMode {
			capture.FlushNonStreamResponse()
		}
		
		// 获取首次响应时间和模式信息
		ttfb := capture.GetFirstByteTime()
		modeStr := "流式"
		if !capture.isStreamMode {
			modeStr = "非流式"
		}
		
		if ttfb != nil {
			common.SysLog(fmt.Sprintf("✅ 账号 %s 请求成功，状态码: %d，TTFB: %v，总耗时: %v，模式: %s，数据大小: %dB", 
				account.Name, capture.statusCode, *ttfb, result.Duration, modeStr, capture.totalDataSize))
		} else {
			common.SysLog(fmt.Sprintf("✅ 账号 %s 请求成功，状态码: %d，总耗时: %v，模式: %s，数据大小: %dB", 
				account.Name, capture.statusCode, result.Duration, modeStr, capture.totalDataSize))
		}
	} else {
		// 失败时获取缓存的错误信息
		result.ErrorMessage = string(capture.GetBufferedData())
		common.SysError(fmt.Sprintf("❌ 账号 %s 请求失败，状态码: %d，耗时: %v，错误: %s", 
			account.Name, capture.statusCode, result.Duration, result.ErrorMessage))
	}

	return result
}

// recordRequest 记录请求历史
func (h *FallbackHandler) recordRequest(accountID uint) {
	h.mu.Lock()
	defer h.mu.Unlock()
	
	now := time.Now()
	h.requestHistory[accountID] = append(h.requestHistory[accountID], now)
	
	// 限制每个账号的历史记录数量为最近100条
	if len(h.requestHistory[accountID]) > 100 {
		h.requestHistory[accountID] = h.requestHistory[accountID][len(h.requestHistory[accountID])-100:]
	}
	
	// 清理超过10分钟的记录
	cutoff := now.Add(-time.Minute * 10)
	for id, times := range h.requestHistory {
		var validTimes []time.Time
		for _, t := range times {
			if t.After(cutoff) {
				validTimes = append(validTimes, t)
			}
		}
		if len(validTimes) == 0 {
			// 如果没有有效记录，删除这个账号的记录
			delete(h.requestHistory, id)
		} else {
			h.requestHistory[id] = validTimes
		}
	}
}

// startCleanupTask 启动定期清理任务
func (h *FallbackHandler) startCleanupTask() {
	h.cleanupTicker = time.NewTicker(time.Hour) // 每小时清理一次
	
	go func() {
		for {
			select {
			case <-h.cleanupTicker.C:
				h.cleanup()
			case <-h.stopChan:
				return
			}
		}
	}()
}

// cleanup 清理过期数据
func (h *FallbackHandler) cleanup() {
	h.mu.Lock()
	defer h.mu.Unlock()
	
	now := time.Now()
	cutoff := now.Add(-time.Hour) // 清理1小时前的数据
	
	// 清理请求历史
	for id, times := range h.requestHistory {
		var validTimes []time.Time
		for _, t := range times {
			if t.After(cutoff) {
				validTimes = append(validTimes, t)
			}
		}
		if len(validTimes) == 0 {
			delete(h.requestHistory, id)
		} else {
			// 限制最大记录数
			if len(validTimes) > 100 {
				validTimes = validTimes[len(validTimes)-100:]
			}
			h.requestHistory[id] = validTimes
		}
	}
	
	// 清理选择器的性能数据
	if adaptiveSelector, ok := h.selector.(*AdaptiveSelector); ok {
		adaptiveSelector.CleanupOldData(cutoff)
	} else if smartSelector, ok := h.selector.(*SmartLoadBalanceSelector); ok {
		if smartSelector.adaptiveSelector != nil {
			smartSelector.adaptiveSelector.CleanupOldData(cutoff)
		}
	}
	
	// 清理健康监控的过期数据
	h.healthMonitor.CleanupStaleData(time.Hour * 24) // 清理24小时前的健康数据
	
	log.Printf("🧹 Fallback清理任务完成，当前请求历史记录数: %d", len(h.requestHistory))
}

// Stop 停止FallbackHandler
func (h *FallbackHandler) Stop() {
	// 发送停止信号
	close(h.stopChan)
	
	// 停止清理定时器
	if h.cleanupTicker != nil {
		h.cleanupTicker.Stop()
	}
	
	// 停止健康监控
	if h.config.EnableHealthCheck && h.healthMonitor != nil {
		h.healthMonitor.Stop()
	}
	
	log.Printf("🛑 FallbackHandler已停止")
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

// StreamingResponseCapture 流式响应捕获器（支持流式和非流式模式）
type StreamingResponseCapture struct {
	gin.ResponseWriter
	statusCode       int
	isSuccess        bool
	buffer           *bytes.Buffer
	headerSet        bool
	headersCopied    bool         // 新增：标记是否已复制响应头
	startTime        time.Time    // 请求开始时间
	firstByteTime    *time.Time   // 首次数据到达时间
	hasReceivedData  bool         // 是否已接收到数据
	isStreamMode     bool         // 是否为流式模式
	totalDataSize    int          // 总数据大小
	upstreamHeaders  http.Header  // 新增：缓存上游响应头
}

// NewStreamingResponseCapture 创建流式响应捕获器
func NewStreamingResponseCapture(writer gin.ResponseWriter, startTime time.Time, requestBody []byte) *StreamingResponseCapture {
	return &StreamingResponseCapture{
		ResponseWriter:  writer,
		statusCode:      200,
		buffer:          bytes.NewBuffer([]byte{}),
		headerSet:       false,
		headersCopied:   false,
		startTime:       startTime,
		hasReceivedData: false,
		isStreamMode:    false, // 初始化为false，等收到响应后再判断
		totalDataSize:   0,
		upstreamHeaders: make(http.Header),
	}
}

// isFallbackStreamResponse 根据响应的Content-Type判断是否为流式响应  
func isFallbackStreamResponse(header http.Header) bool {
	contentType := header.Get("Content-Type")
	isStream := strings.Contains(contentType, "text/event-stream") || 
		   strings.Contains(contentType, "text/plain") ||
		   strings.Contains(contentType, "application/x-ndjson")
	
	// 添加调试日志
	log.Printf("🔍 [Fallback] 响应Content-Type: '%s', 判断为流式: %v", contentType, isStream)
	return isStream
}

// Header 拦截Header方法，缓存上游响应头
func (w *StreamingResponseCapture) Header() http.Header {
	return w.ResponseWriter.Header()
}

// CacheUpstreamHeaders 缓存上游响应头（在非流式模式下使用）
func (w *StreamingResponseCapture) CacheUpstreamHeaders() {
	if !w.isStreamMode {
		// 复制当前响应头到缓存
		for name, values := range w.ResponseWriter.Header() {
			w.upstreamHeaders[name] = values
		}
	}
}

// WriteHeader 捕获状态码并判断是否成功
func (w *StreamingResponseCapture) WriteHeader(statusCode int) {
	log.Printf("🔍 [Fallback] WriteHeader调用: statusCode=%d", statusCode)
	
	w.statusCode = statusCode
	w.isSuccess = statusCode >= 200 && statusCode < 400
	
	// 在收到响应头时根据Content-Type判断是否为流式模式
	if !w.headersCopied {
		log.Printf("🔍 [Fallback] 开始判断流式模式，当前ResponseWriter头部：")
		for name, values := range w.ResponseWriter.Header() {
			log.Printf("🔍 [Fallback]   %s: %v", name, values)
		}
		w.isStreamMode = isFallbackStreamResponse(w.ResponseWriter.Header())
		w.headersCopied = true
	}
	
	log.Printf("🔍 [Fallback] 流式判断结果: isStreamMode=%v, isSuccess=%v", w.isStreamMode, w.isSuccess)
	
	if w.isStreamMode {
		// 流式模式：如果成功立即设置响应头启动流式输出
		if w.isSuccess && !w.headerSet {
			log.Printf("📡 [Fallback] 启动流式输出模式")
			w.ResponseWriter.WriteHeader(statusCode)
			w.headerSet = true
		}
	} else {
		// 非流式模式：先缓存响应头，不立即输出
		log.Printf("📄 [Fallback] 缓存响应头用于非流式输出")
		w.CacheUpstreamHeaders()
	}
}

// Write 写入响应体（根据流式/非流式模式采用不同策略）
func (w *StreamingResponseCapture) Write(data []byte) (int, error) {
	if len(data) == 0 {
		return 0, nil
	}
	
	// 如果还没有判断过流式模式，在首次写入时判断
	if !w.headersCopied {
		w.isStreamMode = isFallbackStreamResponse(w.ResponseWriter.Header())
		w.headersCopied = true
	}
	
	// 记录首次数据到达时间
	if !w.hasReceivedData {
		now := time.Now()
		w.firstByteTime = &now
		w.hasReceivedData = true
	}
	
	// 累计数据大小
	w.totalDataSize += len(data)
	
	if w.isStreamMode {
		// 流式模式：成功时立即输出，失败时缓存
		if w.isSuccess && w.headerSet {
			// 成功的流式输出：直接写入并立即刷新
			n, err := w.ResponseWriter.Write(data)
			if err == nil {
				if flusher, ok := w.ResponseWriter.(interface{ Flush() }); ok {
					flusher.Flush()
				}
			}
			return n, err
		} else if w.isSuccess && !w.headerSet {
			// 首次成功写入：设置响应头后写入
			w.ResponseWriter.WriteHeader(w.statusCode)
			w.headerSet = true
			
			n, err := w.ResponseWriter.Write(data)
			if err == nil {
				if flusher, ok := w.ResponseWriter.(interface{ Flush() }); ok {
					flusher.Flush()
				}
			}
			return n, err
		} else {
			// 失败时缓存数据
			return w.buffer.Write(data)
		}
	} else {
		// 非流式模式：无论成功失败都先缓存，等请求完成后再决定如何处理
		return w.buffer.Write(data)
	}
}

// GetBufferedData 获取缓存的数据
func (w *StreamingResponseCapture) GetBufferedData() []byte {
	return w.buffer.Bytes()
}

// FlushNonStreamResponse 输出非流式响应的缓存数据（仅在成功且非流式模式下调用）
func (w *StreamingResponseCapture) FlushNonStreamResponse() error {
	log.Printf("🔍 [Fallback] FlushNonStreamResponse: isStreamMode=%v, isSuccess=%v", w.isStreamMode, w.isSuccess)
	
	if !w.isStreamMode && w.isSuccess {
		log.Printf("📄 [Fallback] 开始输出非流式响应，数据大小: %d bytes", w.buffer.Len())
		
		// 复制缓存的上游响应头到最终响应，但跳过Content-Type
		for name, values := range w.upstreamHeaders {
			lowerName := strings.ToLower(name)
			if lowerName != "content-length" && lowerName != "content-type" {
				for _, value := range values {
					w.ResponseWriter.Header().Add(name, value)
				}
			}
		}
		
		// 强制设置Content-Type为application/json（非流式响应必须是JSON）
		log.Printf("📄 [Fallback] 强制设置Content-Type为application/json")
		w.ResponseWriter.Header().Set("Content-Type", "application/json")
		
		// 设置响应状态码
		if !w.headerSet {
			w.ResponseWriter.WriteHeader(w.statusCode)
			w.headerSet = true
		}
		
		// 输出所有缓存的数据
		_, err := w.ResponseWriter.Write(w.buffer.Bytes())
		log.Printf("📄 [Fallback] 非流式响应数据已输出完成，错误: %v", err)
		return err
	}
	
	log.Printf("⚠️ [Fallback] 跳过非流式响应输出：isStreamMode=%v, isSuccess=%v", w.isStreamMode, w.isSuccess)
	return nil
}

// GetFirstByteTime 获取首次响应时间(TTFB - Time To First Byte)
func (w *StreamingResponseCapture) GetFirstByteTime() *time.Duration {
	if w.firstByteTime == nil {
		return nil
	}
	ttfb := w.firstByteTime.Sub(w.startTime)
	return &ttfb
}

// min 辅助函数
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}