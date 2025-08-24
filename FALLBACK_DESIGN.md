# 公共Fallback机制设计文档

## 概述

本设计实现了一个生产级别的公共Fallback机制，适用于Claude Code Relay系统中的所有relay服务。该机制提供了智能的账号选择、故障检测、自动恢复和全面的监控功能。

## 架构设计

### 核心组件

1. **FallbackHandler**: 核心处理器，负责执行fallback逻辑
2. **AccountSelector**: 账号选择器，支持多种选择策略
3. **CircuitBreaker**: 熔断器，防止系统过载
4. **HealthMonitor**: 健康监控器，实时监控账号状态
5. **FallbackManager**: 管理器，统一管理所有分组处理器

### 架构图

```
┌─────────────────┐    ┌─────────────────┐    ┌─────────────────┐
│   Controller    │    │   Fallback      │    │   Account       │
│                 │────▶   Manager      │────▶   Selector      │
│  GetMessages() │    │                 │    │                 │
└─────────────────┘    └─────────────────┘    └─────────────────┘
                              │
                              ▼
┌─────────────────┐    ┌─────────────────┐    ┌─────────────────┐
│   Fallback      │    │   Circuit       │    │   Health        │
│   Handler       │────▶   Breaker      │────▶   Monitor       │
│                 │    │                 │    │                 │
└─────────────────┘    └─────────────────┘    └─────────────────┘
                              │
                              ▼
┌─────────────────┐    ┌─────────────────┐    ┌─────────────────┐
│   Relay         │    │   Original      │    │   Database      │
│   Services      │────▶   Handlers      │────▶   Updates       │
│                 │    │                 │    │                 │
└─────────────────┘    └─────────────────┘    └─────────────────┘
```

## 功能特性

### 1. 多种账号选择策略

- **优先级优先**: 按账号优先级和使用次数排序
- **加权策略**: 根据权重和使用情况动态调整
- **轮询策略**: 按轮询方式选择账号
- **最少使用**: 选择使用次数最少的账号
- **混合策略**: 综合考虑多种因素的智能选择
- **自适应策略**: 基于历史性能数据动态调整
- **智能负载均衡**: 结合负载检测的自适应选择

### 2. 智能故障检测

- **熔断器机制**: 防止连续失败导致系统过载
- **健康监控**: 实时监控账号健康状态
- **错误率统计**: 基于历史数据的智能判断
- **响应时间监控**: 检测账号响应性能
- **临时禁用**: 自动临时禁用问题账号

### 3. 自动恢复机制

- **定时健康检查**: 定期检查异常账号状态
- **自动恢复**: 账号恢复正常后自动启用
- **熔断器恢复**: 半开状态探测，成功后恢复
- **性能学习**: 持续学习账号性能特征

### 4. 全面监控和日志

- **详细日志**: 所有操作都有中文日志记录
- **性能指标**: 请求次数、成功率、响应时间等
- **健康状态**: 实时账号健康状态监控
- **配置管理**: 支持运行时配置调整

## 配置说明

### FallbackConfig配置

```go
type FallbackConfig struct {
    MaxRetries           int              `json:"max_retries"`           // 最大重试次数 (0-10)
    RetryDelay           time.Duration    `json:"retry_delay"`           // 重试延迟 (0-5分钟)
    Strategy             FallbackStrategy `json:"strategy"`              // 选择策略
    EnableCircuitBreaker bool             `json:"enable_circuit_breaker"` // 启用熔断器
    CircuitBreakerThreshold int           `json:"circuit_breaker_threshold"` // 熔断器阈值 (0-100)
    FailureWindow       time.Duration    `json:"failure_window"`        // 故障窗口 (1分钟-24小时)
    RecoveryWindow      time.Duration    `json:"recovery_window"`       // 恢复窗口 (1分钟-24小时)
    EnableHealthCheck   bool             `json:"enable_health_check"`   // 启用健康检查
    HealthCheckInterval time.Duration    `json:"health_check_interval"` // 健康检查间隔 (1分钟-1小时)
}
```

### 策略类型

```go
const (
    StrategyPriorityFirst FallbackStrategy = "priority_first"  // 优先级优先策略
    StrategyWeighted      FallbackStrategy = "weighted"       // 加权策略
    StrategyRoundRobin    FallbackStrategy = "round_robin"    // 轮询策略
    StrategyLeastUsed     FallbackStrategy = "least_used"     // 最少使用策略
)
```

## API接口

### 1. 获取Fallback统计信息

```
GET /api/v1/admin/fallback/stats?account_id=1&group_id=1
```

### 2. 更新Fallback配置

```
PUT /api/v1/admin/fallback/config
Content-Type: application/json

{
    "max_retries": 3,
    "retry_delay": "1s",
    "strategy": "priority_first",
    "enable_circuit_breaker": true,
    "circuit_breaker_threshold": 5,
    "failure_window": "5m",
    "recovery_window": "10m",
    "enable_health_check": true,
    "health_check_interval": "2m"
}
```

### 3. 手动禁用账号

```
POST /api/v1/admin/fallback/disable-account
Content-Type: application/json

{
    "group_id": 1,
    "account_id": 1,
    "duration": "10m",
    "reason": "手动维护"
}
```

### 4. 手动启用账号

```
POST /api/v1/admin/fallback/enable-account
Content-Type: application/json

{
    "group_id": 1,
    "account_id": 1
}
```

### 5. 获取账号健康状态

```
GET /api/v1/admin/fallback/account-health?group_id=1&account_id=1
```

### 6. 重置指标

```
POST /api/v1/admin/fallback/reset-metrics
```

### 7. 导出指标数据

```
GET /api/v1/admin/fallback/export-metrics
```

## 使用示例

### 基本使用

```go
// 在Controller中使用
func GetMessages(c *gin.Context) {
    // ... 获取账号列表
    
    // 使用公共fallback机制
    result := relay.HandleWithFallback(c, accounts, body, handleRelayRequest)
    
    // 记录性能数据
    if result.Account != nil {
        relay.UpdateAccountPerformance(groupID, result.Account.ID, result.Success, result.Duration)
    }
    
    // 处理结果
    if !result.Success {
        c.JSON(http.StatusServiceUnavailable, gin.H{
            "message": result.ErrorMessage,
            "fallback_info": map[string]interface{}{
                "attempt_count":  result.AttemptCount,
                "strategy_used":  result.StrategyUsed,
                "failure_reason": result.FailureReason,
            },
        })
    }
}
```

### 自定义策略

```go
// 创建自定义配置
config := &relay.FallbackConfig{
    MaxRetries:           5,
    RetryDelay:           time.Second * 2,
    Strategy:             relay.StrategyWeighted,
    EnableCircuitBreaker: true,
    CircuitBreakerThreshold: 10,
    FailureWindow:       time.Minute * 10,
    RecoveryWindow:      time.Minute * 30,
    EnableHealthCheck:   true,
    HealthCheckInterval: time.Minute * 5,
}

// 初始化fallback管理器
relay.InitFallbackManager(config)
```

## 监控和日志

### 日志示例

```
2024-01-01 10:00:00 🚀 开始处理fallback请求，账号数量: 3，策略: priority_first
2024-01-01 10:00:01 🔄 尝试使用账号 [1/3]: account1 (平台: claude, 优先级: 1)
2024-01-01 10:00:02 ❌ 账号 account1 请求失败: HTTP 429: {"error": "rate_limit"}
2024-01-01 10:00:02 🔄 切换到下一个账号进行重试...
2024-01-01 10:00:03 🔄 尝试使用账号 [2/3]: account2 (平台: claude_console, 优先级: 1)
2024-01-01 10:00:05 ✅ 账号 account2 请求成功，耗时: 2.1s
```

### 健康状态

```json
{
    "account_id": 1,
    "status": "healthy",
    "last_check_time": "2024-01-01T10:00:00Z",
    "success_count": 150,
    "failure_count": 5,
    "avg_response_time": "1.5s",
    "error_rate": 0.032,
    "last_success": "2024-01-01T10:00:00Z",
    "last_failure": "2024-01-01T09:30:00Z"
}
```

### 熔断器状态

```json
{
    "state": "closed",
    "failure_count": 0,
    "last_failure_time": null,
    "consecutive_success": 0,
    "threshold": 5
}
```

## 性能优化

### 1. 内存管理

- 使用sync.RWMutex保护共享数据
- 定期清理过期的性能数据
- 限制请求历史记录的数量

### 2. 并发控制

- 支持高并发的fallback请求
- 使用原子操作更新状态
- 避免锁竞争

### 3. 资源清理

- 定期清理过期的健康数据
- 提供优雅的关闭机制
- 防止内存泄漏

## 故障处理

### 1. 账号故障

- 自动标记为异常状态
- 记录详细的错误信息
- 定期尝试恢复

### 2. 系统故障

- 熔断器保护
- 降级处理
- 自动恢复机制

### 3. 配置错误

- 参数验证
- 默认值保护
- 错误提示

## 扩展性

### 1. 新增策略

```go
type CustomSelector struct {
    // 自定义选择逻辑
}

func (s *CustomSelector) Select(accounts []model.Account) []model.Account {
    // 实现自定义选择逻辑
    return accounts
}
```

### 2. 新增监控指标

```go
// 在FallbackHandler中添加自定义指标
type CustomMetrics struct {
    CustomField1 int64
    CustomField2 float64
}
```

### 3. 插件系统

- 支持插件化的选择器
- 可配置的监控插件
- 扩展的健康检查

## 最佳实践

### 1. 配置建议

- 根据业务特点选择合适的策略
- 合理设置重试次数和延迟
- 启用熔断器保护系统
- 定期检查健康状态

### 2. 监控建议

- 实时监控fallback成功率
- 关注账号健康状态
- 跟踪性能指标变化
- 设置合适的告警阈值

### 3. 运维建议

- 定期清理过期数据
- 监控系统资源使用
- 备份重要配置
- 制定应急处理方案

## 测试方案

### 1. 单元测试

- 测试各种选择策略
- 验证熔断器功能
- 检查健康监控逻辑

### 2. 集成测试

- 测试完整的fallback流程
- 验证与现有系统的集成
- 测试并发场景

### 3. 性能测试

- 测试高并发场景
- 验证内存使用情况
- 测试长时间运行的稳定性

这个公共Fallback机制提供了生产级别的可靠性和扩展性，能够有效提升系统的容错能力和用户体验。