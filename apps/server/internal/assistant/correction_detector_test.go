package assistant

import "testing"

// TestCorrectionDetectorMatchesExplicitWorkflowCorrectionOnly 验证`correctionDetectorMatchesExplicitWorkflowCorrectionOnly`在特定边界条件下的行为，防止同类回归。
func TestCorrectionDetectorMatchesExplicitWorkflowCorrectionOnly(t *testing.T) {
	testCases := []struct {
		name     string
		message  string
		expected bool
		reason   string
	}{
		{
			name:     "match not this intent",
			message:  "不是这个意思，我只是想先看看内容。",
			expected: true,
			reason:   RuntimeCorrectionReasonNotThisIntent,
		},
		{
			name:     "match decline task creation",
			message:  "不用创建任务，先把第三个项目内容列出来。",
			expected: true,
			reason:   RuntimeCorrectionReasonDeclineTaskCreation,
		},
		{
			name:     "match readback only",
			message:  "我只是想先看看内容，不要进入任务流。",
			expected: true,
			reason:   RuntimeCorrectionReasonReadbackOnly,
		},
		{
			name:     "ignore normal workflow request",
			message:  "直接帮我创建任务并开始执行。",
			expected: false,
		},
		{
			name:     "ignore ordinary readback",
			message:  "先把第三个项目内容输出一遍。",
			expected: false,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			signal := DetectExplicitWorkflowCorrection(testCase.message)
			if (signal != nil) != testCase.expected {
				t.Fatalf("expected correction match %v, got %#v", testCase.expected, signal)
			}
			if !testCase.expected {
				return
			}
			if signal.Reason != testCase.reason {
				t.Fatalf("expected correction reason %q, got %q", testCase.reason, signal.Reason)
			}
		})
	}
}
