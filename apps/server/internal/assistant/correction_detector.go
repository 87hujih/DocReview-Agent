package assistant

import "strings"

// RuntimeCorrectionSignal 描述显式 workflow 纠正命中的归一化结果。
type RuntimeCorrectionSignal struct {
	Reason string
}

// DetectExplicitWorkflowCorrection 只识别可解释的显式纠正语句，不承担一般意图识别。
func DetectExplicitWorkflowCorrection(message string) *RuntimeCorrectionSignal {
	normalized := strings.ToLower(strings.TrimSpace(message))
	if normalized == "" {
		return nil
	}

	switch {
	case strings.Contains(normalized, "不是这个意思"):
		return &RuntimeCorrectionSignal{Reason: RuntimeCorrectionReasonNotThisIntent}
	case strings.Contains(normalized, "不用创建任务"),
		strings.Contains(normalized, "不要创建任务"),
		strings.Contains(normalized, "别创建任务"):
		return &RuntimeCorrectionSignal{Reason: RuntimeCorrectionReasonDeclineTaskCreation}
	case strings.Contains(normalized, "我只是想先看看内容"),
		(strings.Contains(normalized, "先看看内容") && strings.Contains(normalized, "不要进入任务流")),
		(strings.Contains(normalized, "只是想先看看") && strings.Contains(normalized, "不要任务")):
		return &RuntimeCorrectionSignal{Reason: RuntimeCorrectionReasonReadbackOnly}
	default:
		return nil
	}
}
