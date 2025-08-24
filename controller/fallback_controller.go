package controller

import (
	"claude-code-relay/common"
	"claude-code-relay/constant"
	"claude-code-relay/relay"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
)

// GetFallbackStats 获取fallback统计信息
func GetFallbackStats(c *gin.Context) {
	accountIDStr := c.Query("account_id")
	groupIDStr := c.Query("group_id")

	stats := make(map[string]interface{})

	if accountIDStr != "" {
		// 获取特定账号的统计信息
		if accountID, err := strconv.ParseUint(accountIDStr, 10, 32); err == nil {
			stats["account_stats"] = relay.GetFallbackStats(uint(accountID))
		}
	}

	if groupIDStr != "" {
		// 获取特定分组的健康统计
		if groupID, err := strconv.Atoi(groupIDStr); err == nil {
			stats["group_health"] = relay.GetGroupHealthStats(groupID)
		}
	}

	if accountIDStr == "" && groupIDStr == "" {
		// 获取全局统计
		stats["global_metrics"] = relay.GetMetrics()
		stats["all_stats"] = relay.ExportMetrics()
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "获取fallback统计信息成功",
		"code":    constant.Success,
		"data":    stats,
	})
}

// UpdateFallbackConfig 更新fallback配置
func UpdateFallbackConfig(c *gin.Context) {
	var config relay.FallbackConfig
	if err := c.ShouldBindJSON(&config); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"message": "请求参数错误: " + err.Error(),
			"code":    constant.InvalidParams,
		})
		return
	}

	// 验证配置参数
	if err := validateFallbackConfig(&config); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"message": "配置参数无效: " + err.Error(),
			"code":    constant.InvalidParams,
		})
		return
	}

	// 更新全局配置
	if relay.GlobalFallbackManager != nil {
		relay.GlobalFallbackManager.UpdateConfig(&config)
	}

	common.SysLog("Fallback配置已更新")

	c.JSON(http.StatusOK, gin.H{
		"message": "更新fallback配置成功",
		"code":    constant.Success,
	})
}

// validateFallbackConfig 验证fallback配置
func validateFallbackConfig(config *relay.FallbackConfig) error {
	if config.MaxRetries < 0 || config.MaxRetries > 10 {
		return fmt.Errorf("MaxRetries必须在0-10之间")
	}

	if config.RetryDelay < 0 || config.RetryDelay > time.Minute*5 {
		return fmt.Errorf("RetryDelay必须在0-5分钟之间")
	}

	if config.CircuitBreakerThreshold < 0 || config.CircuitBreakerThreshold > 100 {
		return fmt.Errorf("CircuitBreakerThreshold必须在0-100之间")
	}

	if config.FailureWindow < time.Minute || config.FailureWindow > time.Hour*24 {
		return fmt.Errorf("FailureWindow必须在1分钟-24小时之间")
	}

	if config.RecoveryWindow < time.Minute || config.RecoveryWindow > time.Hour*24 {
		return fmt.Errorf("RecoveryWindow必须在1分钟-24小时之间")
	}

	if config.HealthCheckInterval < time.Minute || config.HealthCheckInterval > time.Hour {
		return fmt.Errorf("HealthCheckInterval必须在1分钟-1小时之间")
	}

	validStrategies := map[relay.FallbackStrategy]bool{
		relay.StrategyPriorityFirst: true,
		relay.StrategyWeighted:      true,
		relay.StrategyRoundRobin:    true,
		relay.StrategyLeastUsed:     true,
	}

	if !validStrategies[config.Strategy] {
		return fmt.Errorf("无效的Strategy: %s", config.Strategy)
	}

	return nil
}

// DisableAccountManually 手动禁用账号
func DisableAccountManually(c *gin.Context) {
	type DisableRequest struct {
		GroupID   int           `json:"group_id" binding:"required"`
		AccountID uint          `json:"account_id" binding:"required"`
		Duration  time.Duration `json:"duration"` // 禁用时长，默认10分钟
		Reason    string        `json:"reason"`
	}

	var req DisableRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"message": "请求参数错误: " + err.Error(),
			"code":    constant.InvalidParams,
		})
		return
	}

	if req.Duration == 0 {
		req.Duration = time.Minute * 10
	}

	if req.Reason == "" {
		req.Reason = "手动禁用"
	}

	// 禁用账号
	relay.DisableAccount(req.GroupID, req.AccountID, req.Duration, req.Reason)

	common.SysLog(fmt.Sprintf("账号 %d 在分组 %d 中被手动禁用，时长: %v，原因: %s", 
		req.AccountID, req.GroupID, req.Duration, req.Reason))

	c.JSON(http.StatusOK, gin.H{
		"message": fmt.Sprintf("账号已禁用 %v", req.Duration),
		"code":    constant.Success,
	})
}

// EnableAccountManually 手动启用账号
func EnableAccountManually(c *gin.Context) {
	type EnableRequest struct {
		GroupID   int  `json:"group_id" binding:"required"`
		AccountID uint `json:"account_id" binding:"required"`
	}

	var req EnableRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"message": "请求参数错误: " + err.Error(),
			"code":    constant.InvalidParams,
		})
		return
	}

	// 启用账号
	relay.EnableAccount(req.GroupID, req.AccountID)

	common.SysLog(fmt.Sprintf("账号 %d 在分组 %d 中被手动启用", req.AccountID, req.GroupID))

	c.JSON(http.StatusOK, gin.H{
		"message": "账号已启用",
		"code":    constant.Success,
	})
}

// GetAccountHealth 获取账号健康状态
func GetAccountHealth(c *gin.Context) {
	groupIDStr := c.Query("group_id")
	accountIDStr := c.Query("account_id")

	if groupIDStr == "" || accountIDStr == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"message": "缺少必要参数: group_id 和 account_id",
			"code":    constant.InvalidParams,
		})
		return
	}

	groupID, err := strconv.Atoi(groupIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"message": "group_id格式错误",
			"code":    constant.InvalidParams,
		})
		return
	}

	accountID, err := strconv.ParseUint(accountIDStr, 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"message": "account_id格式错误",
			"code":    constant.InvalidParams,
		})
		return
	}

	health := relay.GetAccountHealth(groupID, uint(accountID))
	if health == nil {
		c.JSON(http.StatusNotFound, gin.H{
			"message": "未找到账号健康信息",
			"code":    constant.NotFound,
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "获取账号健康状态成功",
		"code":    constant.Success,
		"data":    health,
	})
}

// ResetMetrics 重置指标
func ResetMetrics(c *gin.Context) {
	relay.ResetMetrics()

	common.SysLog("Fallback指标已重置")

	c.JSON(http.StatusOK, gin.H{
		"message": "指标重置成功",
		"code":    constant.Success,
	})
}

// ExportMetrics 导出指标数据
func ExportMetrics(c *gin.Context) {
	metrics := relay.ExportMetrics()

	c.JSON(http.StatusOK, gin.H{
		"message": "导出指标数据成功",
		"code":    constant.Success,
		"data":    metrics,
	})
}