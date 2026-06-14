package workflowmodule

import "github.com/weedbox/workflow"

// Re-exported domain types so callers can depend on this module alone, without
// also importing github.com/weedbox/workflow directly.
type (
	Application          = workflow.Application
	WorkflowTemplate     = workflow.WorkflowTemplate
	WorkflowStage        = workflow.WorkflowStage
	ReviewLog            = workflow.ReviewLog
	ApproverResolver     = workflow.ApproverResolver
	ApproverResolverFunc = workflow.ApproverResolverFunc
)

// Application status values.
const (
	StatusDraft          = workflow.StatusDraft
	StatusInReview       = workflow.StatusInReview
	StatusReturned       = workflow.StatusReturned
	StatusApproved       = workflow.StatusApproved
	StatusRejectedClosed = workflow.StatusRejectedClosed
)

// Return modes.
const (
	ReturnModeStrict = workflow.ReturnModeStrict
	ReturnModeDirect = workflow.ReturnModeDirect
)

// Review types.
const (
	ReviewTypeSingle = workflow.ReviewTypeSingle
	ReviewTypeAll    = workflow.ReviewTypeAll
)

// Review log actions.
const (
	ActionSubmit      = workflow.ActionSubmit
	ActionApprove     = workflow.ActionApprove
	ActionReturn      = workflow.ActionReturn
	ActionRejectClose = workflow.ActionRejectClose
)

// Engine and store errors.
var (
	ErrInvalidStatus    = workflow.ErrInvalidStatus
	ErrNoPermission     = workflow.ErrNoPermission
	ErrCommentRequired  = workflow.ErrCommentRequired
	ErrTemplateEmpty    = workflow.ErrTemplateEmpty
	ErrTemplateMismatch = workflow.ErrTemplateMismatch
	ErrAlreadyApproved  = workflow.ErrAlreadyApproved
	ErrInvalidTemplate  = workflow.ErrInvalidTemplate
	ErrStaleReturnStage = workflow.ErrStaleReturnStage
	ErrNotFound         = workflow.ErrNotFound
)
