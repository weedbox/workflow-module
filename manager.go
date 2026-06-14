package workflowmodule

import (
	"context"
	"errors"
	"sync"

	"github.com/weedbox/workflow"
	"github.com/weedbox/workflow/gormstore"
)

// ErrNotReady is returned by manager methods called before the module's
// OnStart hook has attached a Store. Inject the manager via Fx and use it from
// your own module's OnStart or later — never from InitDefaultConfigs or other
// pre-start code paths.
var ErrNotReady = errors.New("workflowmodule: manager has no store yet (called before OnStart)")

// WorkflowManager is the user-facing handle for the workflow module. It owns
// the engine, the GORM-backed store, and the service that ties them together.
//
// Concurrency: all exported methods are safe for concurrent use after OnStart.
// SetResolver may be called before or after OnStart; subsequent operations see
// the new resolver immediately because the engine pointer never changes.
type WorkflowManager struct {
	mu      sync.RWMutex
	engine  *workflow.WorkflowEngine
	store   *gormstore.Store
	service *workflow.Service
}

// attach is called by the module's OnStart once the database is available.
func (m *WorkflowManager) attach(store *gormstore.Store) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.store = store
	m.service = workflow.NewService(store, m.engine)
}

// Engine returns the underlying workflow engine. Use this for permission
// queries (CanView, CanReview, CanEditForm, CanResubmit).
func (m *WorkflowManager) Engine() *workflow.WorkflowEngine {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.engine
}

// Store returns the GORM-backed store. Use this for low-level reads (template
// listing, log queries) or when you need to share the transaction with your
// own code via Store.Transact.
func (m *WorkflowManager) Store() *gormstore.Store {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.store
}

// Service returns the workflow.Service that ties engine and store together.
// Prefer the forwarding methods below for everyday use.
func (m *WorkflowManager) Service() *workflow.Service {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.service
}

// SetResolver installs a custom approver resolver on the engine. Calling this
// after the module has accepted live traffic is allowed but caller-coordinated:
// changing the resolver mid-round can affect ALL-stage approval counting.
func (m *WorkflowManager) SetResolver(r workflow.ApproverResolver) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.engine.Resolver = r
}

// service returns the wired service, or ErrNotReady if OnStart has not run.
func (m *WorkflowManager) svc() (*workflow.Service, error) {
	m.mu.RLock()
	svc := m.service
	m.mu.RUnlock()
	if svc == nil {
		return nil, ErrNotReady
	}
	return svc, nil
}

// ----- Forwarding methods -----

// SaveTemplate persists a template and reflows every in-flight application
// bound to it. See workflow.Service.SaveTemplate for reflow semantics.
func (m *WorkflowManager) SaveTemplate(ctx context.Context, tpl workflow.WorkflowTemplate) error {
	svc, err := m.svc()
	if err != nil {
		return err
	}
	return svc.SaveTemplate(ctx, tpl)
}

// CreateDraft persists a new application in Draft status.
func (m *WorkflowManager) CreateDraft(ctx context.Context, app workflow.Application) error {
	svc, err := m.svc()
	if err != nil {
		return err
	}
	return svc.CreateDraft(ctx, app)
}

// Submit moves a Draft application into In_Review at stage 1.
func (m *WorkflowManager) Submit(ctx context.Context, applicationID string) (workflow.Application, workflow.ReviewLog, error) {
	svc, err := m.svc()
	if err != nil {
		return workflow.Application{}, workflow.ReviewLog{}, err
	}
	return svc.Submit(ctx, applicationID)
}

// Approve records a stage approval, advancing the workflow when conditions are met.
func (m *WorkflowManager) Approve(ctx context.Context, applicationID, operatorID string) (workflow.Application, workflow.ReviewLog, error) {
	svc, err := m.svc()
	if err != nil {
		return workflow.Application{}, workflow.ReviewLog{}, err
	}
	return svc.Approve(ctx, applicationID, operatorID)
}

// Return sends the application back to the owner with a required comment.
func (m *WorkflowManager) Return(ctx context.Context, applicationID, operatorID, comment string) (workflow.Application, workflow.ReviewLog, error) {
	svc, err := m.svc()
	if err != nil {
		return workflow.Application{}, workflow.ReviewLog{}, err
	}
	return svc.Return(ctx, applicationID, operatorID, comment)
}

// Withdraw pulls a still-in-review application back to Draft on the owner's
// behalf. Like Submit/Resubmit it trusts the caller to have verified the
// requester is the owner; gate with Engine().CanWithdraw upstream if needed.
func (m *WorkflowManager) Withdraw(ctx context.Context, applicationID, comment string) (workflow.Application, workflow.ReviewLog, error) {
	svc, err := m.svc()
	if err != nil {
		return workflow.Application{}, workflow.ReviewLog{}, err
	}
	return svc.Withdraw(ctx, applicationID, comment)
}

// RevokeApprove cancels operatorID's own pending approval at the current stage.
// Only valid for an ALL stage whose quorum is still outstanding.
func (m *WorkflowManager) RevokeApprove(ctx context.Context, applicationID, operatorID, comment string) (workflow.Application, workflow.ReviewLog, error) {
	svc, err := m.svc()
	if err != nil {
		return workflow.Application{}, workflow.ReviewLog{}, err
	}
	return svc.RevokeApprove(ctx, applicationID, operatorID, comment)
}

// Resubmit puts a Returned application back into the review queue.
func (m *WorkflowManager) Resubmit(ctx context.Context, applicationID string) (workflow.Application, workflow.ReviewLog, error) {
	svc, err := m.svc()
	if err != nil {
		return workflow.Application{}, workflow.ReviewLog{}, err
	}
	return svc.Resubmit(ctx, applicationID)
}

// RejectClose terminally closes the application with a required comment.
func (m *WorkflowManager) RejectClose(ctx context.Context, applicationID, operatorID, comment string) (workflow.Application, workflow.ReviewLog, error) {
	svc, err := m.svc()
	if err != nil {
		return workflow.Application{}, workflow.ReviewLog{}, err
	}
	return svc.RejectClose(ctx, applicationID, operatorID, comment)
}

// GetApplication loads an application snapshot.
func (m *WorkflowManager) GetApplication(ctx context.Context, applicationID string) (workflow.Application, error) {
	svc, err := m.svc()
	if err != nil {
		return workflow.Application{}, err
	}
	return svc.GetApplication(ctx, applicationID)
}

// ListLogs returns the chronological review log for an application.
func (m *WorkflowManager) ListLogs(ctx context.Context, applicationID string) ([]workflow.ReviewLog, error) {
	svc, err := m.svc()
	if err != nil {
		return nil, err
	}
	return svc.ListLogs(ctx, applicationID)
}

// GetTemplate fetches a template definition by ID.
func (m *WorkflowManager) GetTemplate(ctx context.Context, templateID string) (workflow.WorkflowTemplate, error) {
	m.mu.RLock()
	store := m.store
	m.mu.RUnlock()
	if store == nil {
		return workflow.WorkflowTemplate{}, ErrNotReady
	}
	return store.GetTemplate(ctx, templateID)
}
