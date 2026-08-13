package runtime

// RunStatus 为 the 持久化的 lifecycle of 一个 supervised Agent 运行.
type RunStatus string

const (
	RunStatusQueued          RunStatus = "queued"
	RunStatusRunning         RunStatus = "running"
	RunStatusWaitingInput    RunStatus = "waiting_input"
	RunStatusWaitingApproval RunStatus = "waiting_approval"
	RunStatusSucceeded       RunStatus = "succeeded"
	RunStatusFailed          RunStatus = "failed"
	RunStatusCancelled       RunStatus = "cancelled"
)

// StepStatus 为 the 持久化的 lifecycle of 一个类型化的执行节点.
type StepStatus string

const (
	StepStatusQueued          StepStatus = "queued"
	StepStatusRunning         StepStatus = "running"
	StepStatusWaitingInput    StepStatus = "waiting_input"
	StepStatusWaitingApproval StepStatus = "waiting_approval"
	StepStatusSucceeded       StepStatus = "succeeded"
	StepStatusFailed          StepStatus = "failed"
	StepStatusCancelled       StepStatus = "cancelled"
)

// 处理失败： ErrorCategory 为 persisted instead of 一个 unclassified 错误 string.
type ErrorCategory string

const (
	ErrorCategoryInvalidInput      ErrorCategory = "invalid_input"
	ErrorCategoryPermissionDenied  ErrorCategory = "permission_denied"
	ErrorCategoryNotFound          ErrorCategory = "not_found"
	ErrorCategoryConflict          ErrorCategory = "conflict"
	ErrorCategoryRateLimited       ErrorCategory = "rate_limited"
	ErrorCategoryTimeout           ErrorCategory = "timeout"
	ErrorCategoryRetryableUpstream ErrorCategory = "retryable_upstream"
	ErrorCategoryTerminalUpstream  ErrorCategory = "terminal_upstream"
	ErrorCategoryPolicyBlocked     ErrorCategory = "policy_blocked"
	ErrorCategoryCancelled         ErrorCategory = "cancelled"
	ErrorCategoryLeaseExpired      ErrorCategory = "lease_expired"
)

// CanTransitionRun 执行该函数负责的核心处理逻辑。
func CanTransitionRun(from RunStatus, to RunStatus) bool {
	// 根据当前状态或类型选择对应的处理分支。
	switch from {
	case RunStatusQueued:
		return to == RunStatusRunning || to == RunStatusFailed || to == RunStatusCancelled
	case RunStatusRunning:
		return to == RunStatusQueued || to == RunStatusWaitingInput || to == RunStatusWaitingApproval ||
			to == RunStatusSucceeded || to == RunStatusFailed || to == RunStatusCancelled
	case RunStatusWaitingInput, RunStatusWaitingApproval:
		return to == RunStatusQueued || to == RunStatusFailed || to == RunStatusCancelled
	default:
		return false
	}
}

// CanTransitionStep 执行该函数负责的核心处理逻辑。
func CanTransitionStep(from StepStatus, to StepStatus) bool {
	// 根据当前状态或类型选择对应的处理分支。
	switch from {
	case StepStatusQueued:
		return to == StepStatusRunning || to == StepStatusFailed || to == StepStatusCancelled
	case StepStatusRunning:
		return to == StepStatusQueued || to == StepStatusWaitingInput || to == StepStatusWaitingApproval ||
			to == StepStatusSucceeded || to == StepStatusFailed || to == StepStatusCancelled
	case StepStatusWaitingInput, StepStatusWaitingApproval:
		return to == StepStatusQueued || to == StepStatusFailed || to == StepStatusCancelled
	default:
		return false
	}
}

// Retryable 执行该函数负责的核心处理逻辑。
func (category ErrorCategory) Retryable() bool {
	// 根据当前状态或类型选择对应的处理分支。
	switch category {
	case ErrorCategoryRateLimited, ErrorCategoryTimeout, ErrorCategoryRetryableUpstream, ErrorCategoryLeaseExpired:
		return true
	default:
		return false
	}
}

// 有效的执行该函数负责的核心处理逻辑。
func (category ErrorCategory) Valid() bool {
	// 根据当前状态或类型选择对应的处理分支。
	switch category {
	case ErrorCategoryInvalidInput, ErrorCategoryPermissionDenied, ErrorCategoryNotFound,
		ErrorCategoryConflict, ErrorCategoryRateLimited, ErrorCategoryTimeout,
		ErrorCategoryRetryableUpstream, ErrorCategoryTerminalUpstream,
		ErrorCategoryPolicyBlocked, ErrorCategoryCancelled, ErrorCategoryLeaseExpired:
		return true
	default:
		return false
	}
}
