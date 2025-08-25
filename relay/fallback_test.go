package relay

import (
	"claude-code-relay/model"
	"runtime"
	"testing"
	"time"
)

// TestMemoryLeak 测试内存泄漏修复
func TestMemoryLeak(t *testing.T) {
	// 获取初始内存状态
	var m1 runtime.MemStats
	runtime.ReadMemStats(&m1)
	
	// 创建FallbackHandler
	config := &FallbackConfig{
		MaxRetries:           3,
		RetryDelay:           time.Millisecond * 100,
		Strategy:             StrategyPriorityFirst,
		EnableCircuitBreaker: true,
		CircuitBreakerThreshold: 5,
		FailureWindow:       time.Minute * 5,
		RecoveryWindow:      time.Minute * 10,
		EnableHealthCheck:   false, // 关闭健康检查，避免goroutine
		HealthCheckInterval: time.Minute * 2,
	}
	
	handler := NewFallbackHandler(config)
	
	// 模拟大量请求记录
	for i := 0; i < 10000; i++ {
		accountID := uint(i % 100) // 100个不同的账号
		handler.recordRequest(accountID)
		
		// 每1000次请求后，检查内存清理
		if i%1000 == 0 {
			// 强制执行GC
			runtime.GC()
			
			// 检查requestHistory的大小
			handler.mu.RLock()
			historyCount := len(handler.requestHistory)
			handler.mu.RUnlock()
			
			// 验证历史记录数量是否被限制
			if historyCount > 100 {
				t.Errorf("requestHistory过大: %d，应该小于等于100", historyCount)
			}
		}
	}
	
	// 手动触发清理
	handler.cleanup()
	
	// 停止handler
	handler.Stop()
	
	// 强制GC
	runtime.GC()
	time.Sleep(time.Millisecond * 100)
	runtime.GC()
	
	// 获取最终内存状态
	var m2 runtime.MemStats
	runtime.ReadMemStats(&m2)
	
	// 计算内存增长
	memGrowth := m2.Alloc - m1.Alloc
	memGrowthMB := float64(memGrowth) / 1024 / 1024
	
	t.Logf("内存增长: %.2f MB", memGrowthMB)
	
	// 验证内存增长是否在合理范围内（10MB以内）
	if memGrowthMB > 10 {
		t.Errorf("内存增长过大: %.2f MB，可能存在内存泄漏", memGrowthMB)
	}
}

// TestAdaptiveSelectorMemoryLeak 测试AdaptiveSelector内存泄漏
func TestAdaptiveSelectorMemoryLeak(t *testing.T) {
	selector := NewAdaptiveSelector(StrategyPriorityFirst)
	
	// 模拟大量性能数据更新
	for i := 0; i < 5000; i++ {
		accountID := uint(i)
		selector.UpdatePerformance(accountID, true, time.Millisecond*100)
		
		// 每500次更新后，检查数据量
		if i%500 == 0 {
			selector.mu.RLock()
			dataCount := len(selector.performanceData)
			selector.mu.RUnlock()
			
			// 验证性能数据数量是否被限制
			if dataCount > 1000 {
				t.Errorf("performanceData过大: %d，应该小于等于1000", dataCount)
			}
		}
	}
	
	// 清理旧数据
	selector.CleanupOldData(time.Now().Add(-time.Hour))
	
	selector.mu.RLock()
	finalCount := len(selector.performanceData)
	selector.mu.RUnlock()
	
	t.Logf("最终性能数据数量: %d", finalCount)
}

// TestHealthMonitorGracefulShutdown 测试健康监控优雅关闭
func TestHealthMonitorGracefulShutdown(t *testing.T) {
	config := &FallbackConfig{
		EnableHealthCheck:   true,
		HealthCheckInterval: time.Millisecond * 100, // 短间隔便于测试
	}
	
	monitor := NewHealthMonitor(time.Millisecond*100, config)
	
	// 启动健康监控
	go monitor.Start()
	
	// 运行一段时间
	time.Sleep(time.Millisecond * 500)
	
	// 停止监控
	monitor.Stop()
	
	// 等待goroutine退出
	time.Sleep(time.Millisecond * 200)
	
	// 检查goroutine数量（这是一个简单的检查）
	numGoroutines := runtime.NumGoroutine()
	t.Logf("剩余goroutine数量: %d", numGoroutines)
	
	// 基准goroutine数量应该很少（通常测试框架本身会有几个）
	if numGoroutines > 10 {
		t.Logf("警告: goroutine数量较多，可能存在泄漏: %d", numGoroutines)
	}
}

// TestFallbackManagerCleanup 测试FallbackManager清理
func TestFallbackManagerCleanup(t *testing.T) {
	// 初始化管理器
	InitFallbackManager(nil)
	
	// 创建几个处理器
	for i := 1; i <= 5; i++ {
		handler := GlobalFallbackManager.GetHandler(i)
		if handler == nil {
			t.Errorf("无法获取处理器 %d", i)
		}
	}
	
	// 验证处理器已创建
	allHandlers := GlobalFallbackManager.GetAllHandlers()
	if len(allHandlers) != 5 {
		t.Errorf("处理器数量不正确: %d，应该是5", len(allHandlers))
	}
	
	// 清理资源
	GlobalFallbackManager.Cleanup()
	
	// 验证处理器已清理
	allHandlers = GlobalFallbackManager.GetAllHandlers()
	if len(allHandlers) != 0 {
		t.Errorf("清理后仍有处理器: %d，应该是0", len(allHandlers))
	}
	
	t.Log("FallbackManager清理成功")
}

// BenchmarkRequestHistory 基准测试请求历史记录性能
func BenchmarkRequestHistory(b *testing.B) {
	config := &FallbackConfig{
		EnableHealthCheck: false,
	}
	handler := NewFallbackHandler(config)
	defer handler.Stop()
	
	b.ResetTimer()
	
	for i := 0; i < b.N; i++ {
		accountID := uint(i % 100)
		handler.recordRequest(accountID)
	}
	
	b.ReportAllocs()
}

// BenchmarkAdaptiveSelector 基准测试自适应选择器性能
func BenchmarkAdaptiveSelector(b *testing.B) {
	selector := NewAdaptiveSelector(StrategyPriorityFirst)
	
	// 准备测试账号
	accounts := make([]model.Account, 10)
	for i := 0; i < 10; i++ {
		accounts[i] = model.Account{
			ID:              uint(i),
			Priority:        i,
			Weight:          100 - i*10,
			TodayUsageCount: i * 10,
			CurrentStatus:   1,
		}
	}
	
	b.ResetTimer()
	
	for i := 0; i < b.N; i++ {
		selector.Select(accounts)
	}
	
	b.ReportAllocs()
}