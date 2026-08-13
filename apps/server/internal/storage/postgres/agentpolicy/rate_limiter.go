package agentpolicy

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	agenttools "agent_project/apps/server/internal/agent/tools"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type RateLimitRule struct {
	Limit  int
	Window time.Duration
}

type RateLimitRuleResolver interface {
	Resolve(request agenttools.RateLimitRequest) (RateLimitRule, error)
}

type StaticRateLimitRules struct {
	ByTool  map[string]RateLimitRule
	ByRisk  map[agenttools.RiskLevel]RateLimitRule
	Default RateLimitRule
}

// 解析 执行该函数负责的核心处理逻辑。
func (rules StaticRateLimitRules) Resolve(request agenttools.RateLimitRequest) (RateLimitRule, error) {
	if rule, exists := rules.ByTool[request.ToolName+"@"+request.ToolVersion]; exists {
		return validateRateLimitRule(rule)
	}
	if rule, exists := rules.ByRisk[request.RiskLevel]; exists {
		return validateRateLimitRule(rule)
	}
	return validateRateLimitRule(rules.Default)
}

type PostgresRateLimiter struct {
	pool  *pgxpool.Pool
	rules RateLimitRuleResolver
	now   func() time.Time
}

var _ agenttools.RateLimiter = (*PostgresRateLimiter)(nil)

// NewPostgresRateLimiter 校验依赖并创建对应实例。
func NewPostgresRateLimiter(pool *pgxpool.Pool, rules RateLimitRuleResolver) *PostgresRateLimiter {
	return &PostgresRateLimiter{pool: pool, rules: rules, now: func() time.Time { return time.Now().UTC() }}
}

// Allow 执行该函数负责的核心处理逻辑。
func (limiter *PostgresRateLimiter) Allow(ctx context.Context, request agenttools.RateLimitRequest) (agenttools.RateLimitDecision, error) {
	request.ToolName = strings.TrimSpace(request.ToolName)
	request.ToolVersion = strings.TrimSpace(request.ToolVersion)
	if !validPrincipal(request.Security) || request.ToolName == "" || request.ToolVersion == "" {
		return agenttools.RateLimitDecision{}, fmt.Errorf("trusted principal scope and tool identity are required for rate limiting")
	}
	if limiter == nil || limiter.pool == nil || limiter.rules == nil || limiter.now == nil {
		return agenttools.RateLimitDecision{}, fmt.Errorf("持久化的 rate limiter 依赖不能为空")
	}
	rule, err := limiter.rules.Resolve(request)
	if err != nil {
		return agenttools.RateLimitDecision{}, err
	}
	now := limiter.now().UTC()
	bucketStart := fixedWindowStart(now, rule.Window)
	var count int
	err = limiter.pool.QueryRow(ctx, `
		INSERT INTO agent_tool_rate_limit_buckets (
			workspace_id, principal_type, principal_id, tool_name, tool_version, bucket_start, call_count, updated_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, 1, $7)
		ON CONFLICT (workspace_id, principal_type, principal_id, tool_name, tool_version, bucket_start)
		DO UPDATE SET call_count = agent_tool_rate_limit_buckets.call_count + 1, updated_at = EXCLUDED.updated_at
		WHERE agent_tool_rate_limit_buckets.call_count < $8
		RETURNING call_count
	`, request.Security.WorkspaceID, request.Security.PrincipalType, request.Security.PrincipalID,
		request.ToolName, request.ToolVersion, bucketStart, now, rule.Limit).Scan(&count)
	if errors.Is(err, pgx.ErrNoRows) {
		return agenttools.RateLimitDecision{Allowed: false, RetryAfter: bucketStart.Add(rule.Window).Sub(now)}, nil
	}
	if err != nil {
		return agenttools.RateLimitDecision{}, err
	}
	return agenttools.RateLimitDecision{Allowed: count <= rule.Limit}, nil
}

// validateRateLimitRule 校验输入及领域约束。
func validateRateLimitRule(rule RateLimitRule) (RateLimitRule, error) {
	if rule.Limit <= 0 || rule.Window < time.Second || rule.Window > 24*time.Hour {
		return RateLimitRule{}, fmt.Errorf("rate 上限 rule 需要一个正数上限和一个时间窗口来自一个 second 用于 24 hours")
	}
	return rule, nil
}

// fixedWindowStart 执行该函数负责的核心处理逻辑。
func fixedWindowStart(now time.Time, window time.Duration) time.Time {
	return time.Unix(0, (now.UnixNano()/window.Nanoseconds())*window.Nanoseconds()).UTC()
}
