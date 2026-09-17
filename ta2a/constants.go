package ta2a

const (
	// HITL operator commands. Must match HITLConfig.Options default.
	HITLApprove = "approve"
	HITLReject  = "reject"

	// Message metadata key carrying the pre-allocated task ID.
	metaTaskID = "task_id"

	// SSE event names (A2A streaming protocol).
	sseEventStatus   = "status"
	sseEventChunk    = "chunk"
	sseEventArtifact = "artifact"
	sseEventError    = "error"
	sseEventComplete = "complete"

	// State transition reasons. Part of the audit contract — do not change casually.
	reasonTaskStarted    = "task started"
	reasonTaskResumed    = "task resumed with input"
	reasonTaskFinished   = "action execution finished"
	reasonApprovalNeeded = "human approval required"
	reasonOperatorReject = "operation rejected by operator"
	reasonTaskCanceled   = "task canceled by client"

	// Webhook delivery headers.
	headerA2ASignature = "X-A2A-Signature"
	headerA2AAttempt   = "X-A2A-Delivery-Attempt"

	// Task ID prefix used when no metadata task_id is provided.
	taskIDPrefix = "task-"
)
