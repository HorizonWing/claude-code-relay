package relay

import (
	"claude-code-relay/model"
	"sort"
	"time"
)

// PrioritySelector 优先级优先选择器
type PrioritySelector struct{}

func (s *PrioritySelector) Select(accounts []model.Account) []model.Account {
	// 按优先级升序排序，优先级数字越小越高
	sort.Slice(accounts, func(i, j int) bool {
		if accounts[i].Priority != accounts[j].Priority {
			return accounts[i].Priority < accounts[j].Priority
		}
		// 相同优先级按使用次数排序
		return accounts[i].TodayUsageCount < accounts[j].TodayUsageCount
	})
	return accounts
}

// WeightedSelector 加权选择器
type WeightedSelector struct{}

func (s *WeightedSelector) Select(accounts []model.Account) []model.Account {
	// 计算总权重
	totalWeight := 0
	for _, account := range accounts {
		totalWeight += account.Weight
	}

	// 按权重排序，权重越高越优先
	sort.Slice(accounts, func(i, j int) bool {
		// 计算实际权重（考虑使用次数）
		weightI := calculateActualWeight(accounts[i], totalWeight)
		weightJ := calculateActualWeight(accounts[j], totalWeight)
		return weightI > weightJ
	})

	return accounts
}

// RoundRobinSelector 轮询选择器
type RoundRobinSelector struct {
	lastSelected map[uint]int // 记录每个账号最后被选择的轮次
	counter     int          // 轮次计数器
}

func (s *RoundRobinSelector) Select(accounts []model.Account) []model.Account {
	if s.lastSelected == nil {
		s.lastSelected = make(map[uint]int)
	}

	// 为每个账号计算轮询权重
	type accountInfo struct {
		account     model.Account
		roundRobin  int
		lastSelect  int
	}

	infos := make([]accountInfo, len(accounts))
	for i, account := range accounts {
		infos[i] = accountInfo{
			account:     account,
			roundRobin:  s.counter - s.lastSelected[account.ID],
			lastSelect:  s.lastSelected[account.ID],
		}
	}

	// 按轮询权重排序（最久未使用的优先）
	sort.Slice(infos, func(i, j int) bool {
		return infos[i].roundRobin > infos[j].roundRobin
	})

	// 提取排序后的账号
	result := make([]model.Account, len(accounts))
	for i, info := range infos {
		result[i] = info.account
		s.lastSelected[info.account.ID] = s.counter
	}

	s.counter++
	return result
}

// LeastUsedSelector 最少使用选择器
type LeastUsedSelector struct{}

func (s *LeastUsedSelector) Select(accounts []model.Account) []model.Account {
	// 按今日使用次数升序排序
	sort.Slice(accounts, func(i, j int) bool {
		if accounts[i].TodayUsageCount != accounts[j].TodayUsageCount {
			return accounts[i].TodayUsageCount < accounts[j].TodayUsageCount
		}
		// 使用次数相同按优先级排序
		return accounts[i].Priority < accounts[j].Priority
	})
	return accounts
}

// HybridSelector 混合选择器（结合多种策略）
type HybridSelector struct{}

func (s *HybridSelector) Select(accounts []model.Account) []model.Account {
	// 第一步：按优先级分组
	priorityGroups := make(map[int][]model.Account)
	for _, account := range accounts {
		priorityGroups[account.Priority] = append(priorityGroups[account.Priority], account)
	}

	// 第二步：在每个优先级组内按权重排序
	var sortedAccounts []model.Account
	for priority := 1; priority <= 100; priority++ { // 假设优先级范围1-100
		if group, exists := priorityGroups[priority]; exists {
			// 在同优先级组内按权重和健康状态排序
			sort.Slice(group, func(i, j int) bool {
				scoreI := calculateAccountScore(group[i])
				scoreJ := calculateAccountScore(group[j])
				return scoreI > scoreJ
			})
			sortedAccounts = append(sortedAccounts, group...)
		}
	}

	return sortedAccounts
}

// calculateActualWeight 计算账号的实际权重
func calculateActualWeight(account model.Account, totalWeight int) float64 {
	if totalWeight == 0 {
		return 0
	}
	
	baseWeight := float64(account.Weight) / float64(totalWeight)
	
	// 根据使用次数调整权重（使用次数越多，权重相对降低）
	usageFactor := 1.0 - float64(account.TodayUsageCount)/1000.0
	if usageFactor < 0.1 {
		usageFactor = 0.1
	}
	
	return baseWeight * usageFactor
}

// calculateAccountScore 计算账号综合得分
func calculateAccountScore(account model.Account) float64 {
	score := 0.0
	
	// 权重得分 (0-40分)
	score += float64(account.Weight) * 0.4
	
	// 使用次数得分 (0-30分)，使用次数越少得分越高
	usageScore := 30.0
	if account.TodayUsageCount > 0 {
		usageScore = 30.0 * (1.0 - float64(account.TodayUsageCount)/1000.0)
		if usageScore < 0 {
			usageScore = 0
		}
	}
	score += usageScore
	
	// 状态得分 (0-30分)
	switch account.CurrentStatus {
	case 1: // 正常状态
		score += 30
	case 3: // 限流状态
		score += 10
	case 2: // 异常状态
		score += 0
	}
	
	return score
}

// createAccountSelector 创建账号选择器
func createAccountSelector(strategy FallbackStrategy) AccountSelector {
	switch strategy {
	case StrategyPriorityFirst:
		return &PrioritySelector{}
	case StrategyWeighted:
		return &WeightedSelector{}
	case StrategyRoundRobin:
		return &RoundRobinSelector{}
	case StrategyLeastUsed:
		return &LeastUsedSelector{}
	default:
		// 默认使用混合策略
		return &HybridSelector{}
	}
}

// AdaptiveSelector 自适应选择器
type AdaptiveSelector struct {
	strategy        FallbackStrategy
	selector        AccountSelector
	performanceData map[uint]*PerformanceData
}

// PerformanceData 性能数据
type PerformanceData struct {
	SuccessRate    float64       `json:"success_rate"`
	AvgResponseTime time.Duration `json:"avg_response_time"`
	LastSuccess    time.Time     `json:"last_success"`
	LastFailure    time.Time     `json:"last_failure"`
	TotalRequests  int64         `json:"total_requests"`
	TotalSuccesses int64         `json:"total_successes"`
}

func NewAdaptiveSelector(strategy FallbackStrategy) *AdaptiveSelector {
	return &AdaptiveSelector{
		strategy:        strategy,
		selector:        createAccountSelector(strategy),
		performanceData: make(map[uint]*PerformanceData),
	}
}

func (s *AdaptiveSelector) Select(accounts []model.Account) []model.Account {
	// 基础排序
	sortedAccounts := s.selector.Select(accounts)
	
	// 根据性能数据微调
	if len(s.performanceData) > 0 {
		s.adjustByPerformance(sortedAccounts)
	}
	
	return sortedAccounts
}

func (s *AdaptiveSelector) adjustByPerformance(accounts []model.Account) {
	// 根据性能数据调整账号顺序
	sort.Slice(accounts, func(i, j int) bool {
		perfI := s.performanceData[accounts[i].ID]
		perfJ := s.performanceData[accounts[j].ID]
		
		if perfI == nil && perfJ == nil {
			return false
		}
		if perfI == nil {
			return false
		}
		if perfJ == nil {
			return true
		}
		
		// 比较成功率
		if perfI.SuccessRate != perfJ.SuccessRate {
			return perfI.SuccessRate > perfJ.SuccessRate
		}
		
		// 成功率相同比较响应时间
		return perfI.AvgResponseTime < perfJ.AvgResponseTime
	})
}

func (s *AdaptiveSelector) UpdatePerformance(accountID uint, success bool, responseTime time.Duration) {
	perf := s.performanceData[accountID]
	if perf == nil {
		perf = &PerformanceData{}
		s.performanceData[accountID] = perf
	}
	
	perf.TotalRequests++
	if success {
		perf.TotalSuccesses++
		perf.LastSuccess = time.Now()
	} else {
		perf.LastFailure = time.Now()
	}
	
	perf.SuccessRate = float64(perf.TotalSuccesses) / float64(perf.TotalRequests)
	
	// 更新平均响应时间
	if perf.AvgResponseTime == 0 {
		perf.AvgResponseTime = responseTime
	} else {
		perf.AvgResponseTime = (perf.AvgResponseTime + responseTime) / 2
	}
}

// SmartLoadBalanceSelector 智能负载均衡选择器
type SmartLoadBalanceSelector struct {
	adaptiveSelector *AdaptiveSelector
	loadThreshold    float64 // 负载阈值
}

func NewSmartLoadBalanceSelector(strategy FallbackStrategy, loadThreshold float64) *SmartLoadBalanceSelector {
	return &SmartLoadBalanceSelector{
		adaptiveSelector: NewAdaptiveSelector(strategy),
		loadThreshold:    loadThreshold,
	}
}

func (s *SmartLoadBalanceSelector) Select(accounts []model.Account) []model.Account {
	// 计算每个账号的负载分数
	type accountLoad struct {
		account      model.Account
		loadScore    float64
		isOverloaded bool
	}
	
	loads := make([]accountLoad, len(accounts))
	for i, account := range accounts {
		loadScore := s.calculateLoadScore(account)
		loads[i] = accountLoad{
			account:      account,
			loadScore:    loadScore,
			isOverloaded: loadScore > s.loadThreshold,
		}
	}
	
	// 过滤掉过载的账号
	var availableAccounts []model.Account
	for _, load := range loads {
		if !load.isOverloaded {
			availableAccounts = append(availableAccounts, load.account)
		}
	}
	
	// 如果所有账号都过载，使用所有账号但按负载排序
	if len(availableAccounts) == 0 {
		for _, load := range loads {
			availableAccounts = append(availableAccounts, load.account)
		}
		sort.Slice(availableAccounts, func(i, j int) bool {
			return loads[i].loadScore < loads[j].loadScore
		})
	} else {
		// 使用自适应选择器
		availableAccounts = s.adaptiveSelector.Select(availableAccounts)
	}
	
	return availableAccounts
}

func (s *SmartLoadBalanceSelector) calculateLoadScore(account model.Account) float64 {
	score := 0.0
	
	// 基于使用次数的负载 (0-50分)
	maxUsage := 1000.0 // 假设最大使用次数
	usageLoad := float64(account.TodayUsageCount) / maxUsage * 50
	if usageLoad > 50 {
		usageLoad = 50
	}
	score += usageLoad
	
	// 基于状态的负载 (0-30分)
	switch account.CurrentStatus {
	case 1: // 正常
		score += 0
	case 3: // 限流
		score += 20
	case 2: // 异常
		score += 30
	}
	
	// 基于权重的负载调整 (0-20分)
	if account.Weight > 0 {
		weightLoad := (100.0 / float64(account.Weight)) * 20
		if weightLoad > 20 {
			weightLoad = 20
		}
		score += weightLoad
	}
	
	return score
}

func (s *SmartLoadBalanceSelector) UpdatePerformance(accountID uint, success bool, responseTime time.Duration) {
	s.adaptiveSelector.UpdatePerformance(accountID, success, responseTime)
}